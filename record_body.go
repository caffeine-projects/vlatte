package vlatte

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
)

const (
	// _spoolChunk is the size of the chunks a body hands to its spool goroutine: small writes fill one first.
	_spoolChunk = 32 << 10

	// _spoolQueue is how many chunks wait for the disk before the sender waits for it: 2 MiB, room for the file to
	// be created and for the disk to catch up.
	_spoolQueue = 64

	// _tempPattern names the files being written: never *.mock.*, so loading mock files skips them.
	_tempPattern = ".rec-*.tmp"

	// _slugMax bounds the part of a file name that comes from a mock's name.
	_slugMax = 80
)

var (
	errBodyTooLarge  = errors.New("recorder: the body is larger than MaxBodySize")
	errDiskTooSlow   = errors.New("recorder: the disk fell behind the body")
	errBodyStopped   = errors.New("recorder: the recording stopped")
	errBodyDiscarded = errors.New("recorder: the body was discarded")
	errBodyOpen      = errors.New("recorder: the body is still being captured")
	errBodyInMemory  = errors.New("recorder: the body stayed in memory: place its bytes")
	errBodyPlaced    = errors.New("recorder: the body was placed already")
)

// _spoolWait is how long a body waits for the disk in all, beyond which it isn't kept: a sender faster than the disk,
// such as a local one, goes at the disk's pace that long at most. Tests shorten it.
var _spoolWait = time.Second

// _chunks recycles the chunks a body copies what it spools into.
var _chunks = sync.Pool{New: func() any {
	b := make([]byte, 0, _spoolChunk)
	return &b
}}

// spoolFile is the file a body spools to.
type spoolFile interface {
	io.WriteCloser
	Name() string
}

// _openSpool creates the file a body spools to, in dir. Tests replace it with a slow disk.
var _openSpool = func(dir string) (spoolFile, error) {
	return os.CreateTemp(dir, _tempPattern)
}

// bodyState is where a RecordBody is in its life.
type bodyState int

const (
	bodyCapturing bodyState = iota
	bodyClosed
	bodyPlaced
	bodyDropped
)

// recordDir is what the bodies of a session share: where they spool, their limits, and the goroutines that spool.
type recordDir struct {
	dir       string
	maxInline int64
	maxBody   int64
	bodyFiles bool
	logger    *zerolog.Logger
	spools    sync.WaitGroup

	mu      sync.Mutex
	live    map[*RecordBody]struct{} // bodies still being captured
	stopped bool
	done    chan struct{} // closed when stopped: a body waiting for the disk gives up
}

// newBody returns a body to capture into. Once the recording stopped, the body keeps nothing.
func (d *recordDir) newBody() *RecordBody {
	b := &RecordBody{dir: d}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stopped {
		b.state, b.err = bodyDropped, errBodyStopped
		return b
	}

	d.live[b] = struct{}{}

	return b
}

// stop drops the bodies still being captured: their calls end after the recording. A body whose call ended since it
// listed them is whole: its call may be recorded already.
func (d *recordDir) stop() {
	d.mu.Lock()
	live := make([]*RecordBody, 0, len(d.live))
	for b := range d.live {
		live = append(live, b)
	}

	if !d.stopped {
		d.stopped = true
		close(d.done)
	}

	d.mu.Unlock()

	for _, b := range live {
		b.stopCapture()
	}
}

func (d *recordDir) forget(b *RecordBody) {
	d.mu.Lock()
	delete(d.live, b)
	d.mu.Unlock()
}

// spool is a body being written to a file, by a goroutine of its own.
type spool struct {
	chunks chan []byte
	done   chan struct{} // closed once the file is written, or removed

	// The goroutine and a drop agree on who removes the file: whoever comes second.
	mu       sync.Mutex
	dropped  bool
	finished bool
	temp     string // the file, once written whole
	sum      string // the hex sha256 of what it holds
	err      error
}

func (d *recordDir) startSpool() *spool {
	sp := &spool{chunks: make(chan []byte, _spoolQueue), done: make(chan struct{})}
	d.spools.Go(func() { sp.run(d.dir) })

	return sp
}

// run writes the chunks to a new file of dir and hashes them, until the body is closed or dropped. A dropped body's
// file is removed.
func (sp *spool) run(dir string) {
	defer close(sp.done)

	f, err := _openSpool(dir)
	if err != nil {
		for chunk := range sp.chunks {
			putChunk(chunk)
		}

		sp.err = err

		return
	}

	h := sha256.New()

	for chunk := range sp.chunks {
		if err == nil && !sp.isDropped() {
			if _, err = f.Write(chunk); err == nil {
				h.Write(chunk)
			}
		}

		putChunk(chunk)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()

	sp.finished = true

	if err != nil || sp.dropped {
		os.Remove(f.Name())
		sp.err = err

		return
	}

	sp.temp, sp.sum = f.Name(), hex.EncodeToString(h.Sum(nil))
}

func (sp *spool) isDropped() bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	return sp.dropped
}

// drop removes the file once written, or has the goroutine skip and remove it.
func (sp *spool) drop() {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	sp.dropped = true

	if sp.finished && sp.temp != "" {
		os.Remove(sp.temp)
		sp.temp = ""
	}
}

// RecordBody captures a body as it is sent. It keeps it in memory up to MaxInlineBody. Past that, a goroutine of its
// own writes it to a file of the recording's directory, so the size of the body never holds memory. A sender faster
// than the disk waits for it, a second in all at most: a body the disk falls further behind, or larger than
// MaxBodySize, isn't kept. Its call isn't recorded, and Err says why.
type RecordBody struct {
	dir *recordDir

	mu     sync.Mutex
	state  bodyState
	err    error
	mem    []byte
	size   int64
	spool  *spool
	chunk  []byte        // the chunk being filled, once spooling
	waited time.Duration // how long the sender waited for the disk
	timer  *time.Timer
}

// Keep keeps p, the next bytes of the body. It waits for the disk only when the body goes to its file faster than the
// disk takes it, as the type says. It does nothing once the body is closed or dropped. p is the caller's again when
// Keep returns.
func (b *RecordBody) Keep(p []byte) {
	if len(p) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != bodyCapturing {
		return
	}

	b.size += int64(len(p))
	if b.size > b.dir.maxBody {
		b.dropLocked(errBodyTooLarge)
		return
	}

	if b.spool == nil {
		if int64(len(b.mem)+len(p)) <= b.dir.maxInline {
			b.mem = append(b.mem, p...)
			return
		}

		// The first chunk is what stayed in memory: the queue is empty, so it never waits.
		b.spool = b.dir.startSpool()
		if len(b.mem) > 0 {
			b.spool.chunks <- b.mem
		}

		b.mem = nil
	}

	for len(p) > 0 {
		if b.chunk == nil {
			b.chunk = getChunk()
		}

		n := min(len(p), _spoolChunk-len(b.chunk))
		b.chunk = append(b.chunk, p[:n]...)
		p = p[n:]

		if len(b.chunk) == _spoolChunk && !b.sendLocked() {
			return
		}
	}
}

// Close ends the body: it keeps nothing more.
func (b *RecordBody) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != bodyCapturing {
		return
	}

	if b.spool != nil && len(b.chunk) > 0 && !b.sendLocked() {
		return
	}

	b.state = bodyClosed
	if b.spool != nil {
		close(b.spool.chunks)
	}

	b.dir.forget(b)
}

// Discard drops the body, and the file it went to: its call won't be recorded. A placed body is the recording's.
func (b *RecordBody) Discard() {
	b.drop(errBodyDiscarded)
}

// Bytes returns the body once closed, when it stayed in memory. It returns false when it went to a file, or wasn't
// kept.
func (b *RecordBody) Bytes() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != bodyClosed || b.spool != nil {
		return nil, false
	}

	return b.mem, true
}

// Len returns how many bytes the body had.
func (b *RecordBody) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.size
}

// Err returns why the body wasn't kept, or nil.
func (b *RecordBody) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == bodyDropped {
		return b.err
	}

	return nil
}

// sendLocked hands the chunk being filled to the spool goroutine. When the disk is behind, the sender waits for it,
// until the body waited _spoolWait in all: then, or when the recording stops, the body is dropped.
func (b *RecordBody) sendLocked() bool {
	chunk := b.chunk
	b.chunk = nil

	select {
	case b.spool.chunks <- chunk:
		return true
	default:
	}

	err := errDiskTooSlow

	if left := _spoolWait - b.waited; left > 0 {
		if b.timer == nil {
			b.timer = time.NewTimer(left)
		} else {
			b.timer.Reset(left)
		}

		start := time.Now()

		select {
		case b.spool.chunks <- chunk:
			b.timer.Stop()
			b.waited += time.Since(start)

			return true
		case <-b.dir.done:
			b.timer.Stop()
			err = errBodyStopped
		case <-b.timer.C:
		}
	}

	putChunk(chunk)
	b.dropLocked(err)

	return false
}

func (b *RecordBody) drop(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.dropLocked(err)
}

// stopCapture drops the body when it is still being captured, as the recording stops.
func (b *RecordBody) stopCapture() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == bodyCapturing {
		b.dropLocked(errBodyStopped)
	}
}

// dropLocked drops the body. Its file, if any, is removed once its goroutine is done.
func (b *RecordBody) dropLocked(err error) {
	if b.state == bodyDropped || b.state == bodyPlaced {
		return
	}

	capturing := b.state == bodyCapturing
	b.state, b.err, b.mem = bodyDropped, err, nil

	if b.chunk != nil {
		putChunk(b.chunk)
		b.chunk = nil
	}

	if capturing {
		b.dir.forget(b)
	}

	if b.spool == nil {
		return
	}

	b.spool.drop()

	if capturing {
		close(b.spool.chunks)
	}

	if errors.Is(err, errBodyTooLarge) || errors.Is(err, errDiskTooSlow) {
		b.dir.logger.Warn().Err(err).Msg("recorder: a body wasn't kept: its call isn't recorded")
	}
}

// RecordFiles places the bodies of one call: inline in its mock file, or in files next to it, written with the mock.
// Files are named after their content, so equal bodies share one.
type RecordFiles struct {
	dir     *recordDir
	slug    string
	pending []pendingFile
}

// pendingFile is a body file to write: bytes, or a spooled file to move into place.
type pendingFile struct {
	name string
	data []byte
	temp string
}

// Place returns where body goes. It goes inline when text is true and the recording neither writes every body to a
// file nor finds body longer than MaxInlineBody; otherwise to a file next to the mock file, whose name it returns, and
// which ext ends, such as ".json". Place keeps body until the mock is written: the caller doesn't change it after.
func (f *RecordFiles) Place(body []byte, text bool, ext string) (name string, inline bool) {
	if text && !f.dir.bodyFiles && int64(len(body)) <= f.dir.maxInline {
		return "", true
	}

	sum := sha256.Sum256(body)
	name = f.slug + "-" + hex.EncodeToString(sum[:])[:16] + cleanExt(ext)
	f.add(pendingFile{name: name, data: body})

	return name, false
}

// PlaceSpooled places a body that went to a file while it was captured: the file moves next to the mock file, under
// the name it returns, which ext ends. It waits for the file to be written. It fails when the body wasn't kept, or
// stayed in memory: Place takes its Bytes.
func (f *RecordFiles) PlaceSpooled(b *RecordBody, ext string) (string, error) {
	b.mu.Lock()

	switch {
	case b.state == bodyDropped:
		err := b.err
		b.mu.Unlock()

		return "", err
	case b.state == bodyCapturing:
		b.mu.Unlock()
		return "", errBodyOpen
	case b.state == bodyPlaced:
		b.mu.Unlock()
		return "", errBodyPlaced
	case b.spool == nil:
		b.mu.Unlock()
		return "", errBodyInMemory
	}

	sp := b.spool
	b.state = bodyPlaced
	b.mu.Unlock()

	<-sp.done

	if sp.err != nil {
		return "", sp.err
	}

	name := f.slug + "-" + sp.sum[:16] + cleanExt(ext)
	f.add(pendingFile{name: name, temp: sp.temp})

	return name, nil
}

func (f *RecordFiles) add(file pendingFile) {
	for _, p := range f.pending {
		if p.name == file.name {
			if file.temp != "" {
				os.Remove(file.temp)
			}

			return
		}
	}

	f.pending = append(f.pending, file)
}

// discard drops the files placed: their call isn't recorded.
func (f *RecordFiles) discard() {
	for _, p := range f.pending {
		if p.temp != "" {
			os.Remove(p.temp)
		}
	}

	f.pending = nil
}

// recordSlug turns the name of a mock into the start of its file names: letters, digits, '.', '_' and '-', with one
// '-' for each run of anything else, at most _slugMax bytes, and never empty or starting with a '.'.
func recordSlug(name string) string {
	var b strings.Builder

	dash := false
	for _, r := range name {
		if b.Len() >= _slugMax {
			break
		}

		if r < utf8.RuneSelf && (isAlnum(byte(r)) || r == '.' || r == '_' || r == '-') {
			b.WriteRune(r)
			dash = r == '-'

			continue
		}

		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}

	slug := strings.Trim(b.String(), "-.")
	if slug == "" {
		return "recording"
	}

	return slug
}

// cleanExt returns ext when it is a file extension of letters and digits, such as ".json", and ".bin" otherwise.
func cleanExt(ext string) string {
	rest, ok := strings.CutPrefix(ext, ".")
	if !ok || rest == "" || len(rest) > 10 {
		return ".bin"
	}

	for i := range len(rest) {
		if !isAlnum(rest[i]) {
			return ".bin"
		}
	}

	return ext
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

func getChunk() []byte {
	b, _ := _chunks.Get().(*[]byte)
	return (*b)[:0]
}

// putChunk recycles a chunk that came from getChunk; others, such as the first one of a body, aren't pooled.
func putChunk(chunk []byte) {
	if cap(chunk) == _spoolChunk {
		_chunks.Put(&chunk)
	}
}
