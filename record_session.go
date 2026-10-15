package vlatte

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

const (
	_defaultMaxInlineBody = 1 << 20
	_defaultMaxBodySize   = 1 << 30

	// _minRecordBudget is the least memory the calls waiting to be written may hold: 16 bodies of MaxInlineBody when
	// that is more.
	_minRecordBudget = 64 << 20

	// _recordFlushInterval spaces the writes of a mock: a request that repeats rewrites its file once per interval.
	_recordFlushInterval = 250 * time.Millisecond

	// _maxRecordErrors bounds the failures Stop returns, then counts the rest.
	_maxRecordErrors = 10

	// _maxResponses bounds the responses a mock keeps: the later ones of a request that keeps changing its answer,
	// such as a clock, aren't recorded.
	_maxResponses = 1000

	_dropLogInterval = time.Second

	// _staleTemp is how old a temporary file of a directory must be for a new recording to remove it: younger ones
	// may be another recording's, still being written.
	_staleTemp = time.Hour
)

// RecordSession records the calls of a protocol as mock files, off the request path. Record queues a call and returns
// at once; a goroutine of the session turns calls into mocks and writes them. Calls of one request make one mock.
type RecordSession[X, Q, S any] struct {
	codec  RecordCodec[X, Q, S]
	opts   RecordOptions[Q, S]
	format string
	budget int64
	logger *zerolog.Logger
	files  *recordDir

	mu       sync.Mutex
	queue    []queuedCall[X]
	queued   int64 // bytes the queue holds
	stopping bool
	wake     chan struct{}
	done     chan struct{}

	calls    atomic.Int64
	mocks    atomic.Int64
	dropped  atomic.Int64
	failed   atomic.Int64
	lastDrop atomic.Int64 // unix nanoseconds of the last drop logged

	// The worker's, until done closes.
	groups map[string]*recordGroup
	order  []*recordGroup
	dirty  map[*recordGroup]struct{}
	errs   []error
	more   int // failures past _maxRecordErrors
}

// NewRecordSession starts a recording into o.Dir, which it creates when missing. It removes the files a recording
// that ended unexpectedly left half written there: one recording writes to a directory at a time.
func NewRecordSession[X, Q, S any](c RecordCodec[X, Q, S], o RecordOptions[Q, S]) (*RecordSession[X, Q, S], error) {
	if c == nil {
		return nil, errors.New("recorder: no codec")
	}

	format, ok := config.Format(o.Format)
	if !ok {
		return nil, fmt.Errorf("recorder: format %q is not json, yaml or yml", o.Format)
	}

	switch {
	case o.Dir == "":
		return nil, errors.New("recorder: no directory to record into")
	case o.Repeats != RecordSequence && o.Repeats != RecordFirst:
		return nil, fmt.Errorf("recorder: repeats %d is not RecordSequence or RecordFirst", o.Repeats)
	case o.MaxInlineBody < 0:
		return nil, fmt.Errorf("recorder: MaxInlineBody must be 0 or more, not %d", o.MaxInlineBody)
	}

	maxInline := cmp.Or(o.MaxInlineBody, _defaultMaxInlineBody)

	maxBody := cmp.Or(o.MaxBodySize, _defaultMaxBodySize)
	if maxBody < 0 {
		maxBody = math.MaxInt64
	}

	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("recorder: %w", err)
	}

	removeStaleTemps(o.Dir)

	logger := o.Logger
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}

	s := &RecordSession[X, Q, S]{
		codec:  c,
		opts:   o,
		format: format,
		budget: max(_minRecordBudget, 16*maxInline),
		logger: logger,
		files: &recordDir{
			dir:       o.Dir,
			maxInline: maxInline,
			maxBody:   maxBody,
			bodyFiles: o.BodyFiles,
			logger:    logger,
			live:      make(map[*RecordBody]struct{}),
			done:      make(chan struct{}),
		},
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		groups: make(map[string]*recordGroup),
		dirty:  make(map[*recordGroup]struct{}),
	}

	go s.run()

	return s, nil
}

// MustNewRecordSession is NewRecordSession, and panics when it fails.
func MustNewRecordSession[X, Q, S any](c RecordCodec[X, Q, S], o RecordOptions[Q, S]) *RecordSession[X, Q, S] {
	s, err := NewRecordSession(c, o)
	if err != nil {
		panic(err)
	}

	return s
}

// NewBody returns a body to capture into, for a call of this session.
func (s *RecordSession[X, Q, S]) NewBody() *RecordBody {
	return s.files.newBody()
}

// Record queues x, a call to record, and returns at once. size is what x holds in memory. It returns false when the
// recording stopped, or when the calls waiting to be written hold too much already. A call the session doesn't
// record is discarded: when X has a Discard method, the session calls it.
func (s *RecordSession[X, Q, S]) Record(x X, size int) bool {
	s.mu.Lock()

	if s.stopping {
		s.mu.Unlock()
		discard(x)

		return false
	}

	// A call is taken whatever its size when nothing waits: the budget can't refuse every call.
	if len(s.queue) > 0 && s.queued+int64(size) > s.budget {
		s.mu.Unlock()
		s.dropped.Add(1)
		s.logDrop()
		discard(x)

		return false
	}

	s.queue = append(s.queue, queuedCall[X]{x: x, size: int64(size)})
	s.queued += int64(size)
	s.mu.Unlock()

	s.calls.Add(1)

	select {
	case s.wake <- struct{}{}:
	default:
	}

	return true
}

// Stop stops taking calls, writes those it took, and returns what it wrote. Calls still being captured aren't
// recorded. It waits for the writing, or for ctx: when ctx ends first, it returns ctx's error, and a later Stop waits
// again. Done closes once the writing ends. The error joins the calls and mocks that failed.
func (s *RecordSession[X, Q, S]) Stop(ctx context.Context) (*RecordResult, error) {
	// The bodies still being captured go first: none starts writing to a file once the worker can see the stop.
	s.files.stop()

	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}

	select {
	case <-s.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return s.result(), s.err()
}

// Status counts the calls of the recording.
func (s *RecordSession[X, Q, S]) Status() RecordStatus {
	s.mu.Lock()
	active := !s.stopping
	s.mu.Unlock()

	return RecordStatus{
		Active:  active,
		Calls:   s.calls.Load(),
		Mocks:   s.mocks.Load(),
		Dropped: s.dropped.Load(),
		Failed:  s.failed.Load(),
	}
}

// Done closes once the recording stopped and wrote everything it took.
func (s *RecordSession[X, Q, S]) Done() <-chan struct{} {
	return s.done
}

// run takes the calls in the order they came, and writes the mocks they change at most every _recordFlushInterval,
// until the recording stops.
func (s *RecordSession[X, Q, S]) run() {
	defer close(s.done)
	defer s.files.spools.Wait()

	var (
		timer *time.Timer
		tick  <-chan time.Time
	)

	for {
		select {
		case <-s.wake:
		case <-tick:
			tick = nil
			s.flush()
		}

		batch, stopping := s.take()
		for _, c := range batch {
			s.add(c.x)
		}

		if stopping {
			s.flush()
			s.dropUnwritten()

			return
		}

		if len(s.dirty) > 0 && tick == nil {
			if timer == nil {
				timer = time.NewTimer(_recordFlushInterval)
			} else {
				timer.Reset(_recordFlushInterval)
			}

			tick = timer.C
		}
	}
}

// take takes the calls waiting, and reports whether the recording stopped: then no call comes after them.
func (s *RecordSession[X, Q, S]) take() ([]queuedCall[X], bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch := s.queue
	s.queue, s.queued = nil, 0

	return batch, s.stopping
}

// add turns x into a response of the mock of its request.
func (s *RecordSession[X, Q, S]) add(x X) {
	files := &RecordFiles{dir: s.files}

	defer func() {
		if r := recover(); r != nil {
			files.discard()
			discard(x)
			s.fail(fmt.Errorf("recorder: recording a call panicked: %v", r))
		}
	}()

	name := s.codec.Name(x)
	files.slug = recordSlug(name)

	q, err := s.codec.Request(x, files)
	if err == nil {
		var request []byte
		if request, err = json.Marshal(q, json.Deterministic(true)); err == nil {
			err = s.addResponse(x, name, request, files)
		}
	}

	if err != nil {
		files.discard()
		discard(x)

		// A call still being captured when the recording stopped isn't recorded, as Stop says: it didn't fail.
		if errors.Is(err, errBodyStopped) {
			s.logger.Debug().Str("call", name).Msg("recorder: not recorded: the recording stopped during the call")
			return
		}

		s.fail(fmt.Errorf("recorder: %s: %w", name, err))
	}
}

// addResponse adds the response of x to the mock of request. A repeat that RecordFirst ignores adds nothing.
func (s *RecordSession[X, Q, S]) addResponse(x X, name string, request []byte, files *RecordFiles) error {
	sum := sha256.Sum256(request)
	key := hex.EncodeToString(sum[:])

	g, seen := s.groups[key]
	if seen && s.opts.Repeats == RecordFirst {
		files.discard()
		discard(x)

		return nil
	}

	r, err := s.codec.Response(x, files)
	if err != nil {
		return err
	}

	response, err := json.Marshal(r, json.Deterministic(true))
	if err != nil {
		return err
	}

	if !seen {
		g = &recordGroup{id: files.slug + "-" + key[:16], name: name, request: request}
		s.groups[key] = g
		s.order = append(s.order, g)
	}

	changed, kept := g.add(response)
	if !kept && !g.warned {
		g.warned = true
		s.logger.Warn().Str("mock", g.id).Msgf("recorder: the mock has %d responses: later ones aren't recorded", _maxResponses)
	}

	if changed {
		s.dirty[g] = struct{}{}
	}

	if !kept {
		files.discard()
		discard(x)

		return nil
	}

	g.keep(files)

	return nil
}

// flush writes the mocks that changed since the last flush. A mock the disk failed to take stays to write at the next
// flush; one that can't be encoded, such as one a transform refuses, waits for its request to come again.
func (s *RecordSession[X, Q, S]) flush() {
	// The directory may have gone, such as removed while the recording ran.
	if len(s.dirty) > 0 {
		_ = os.MkdirAll(s.opts.Dir, 0o755)
	}

	for g := range s.dirty {
		if !s.write(g) {
			delete(s.dirty, g)
		}
	}
}

// write writes the mock of g: its body files, then its mock file. It reports whether to try again: the disk failed.
func (s *RecordSession[X, Q, S]) write(g *recordGroup) (retry bool) {
	data, name, err := s.encode(g)
	if err != nil {
		s.failMock(g, fmt.Errorf("recorder: %s: %w", g.id, err))
		return false
	}

	for len(g.pending) > 0 {
		p := g.pending[0]
		if err := s.writeBody(p); err != nil {
			s.failMock(g, fmt.Errorf("recorder: %s: body file %s: %w", g.id, p.name, err))
			return true
		}

		if !slices.Contains(g.bodyFiles, p.name) {
			g.bodyFiles = append(g.bodyFiles, p.name)
		}

		// What was written is the file's: the bytes go.
		g.pending[0] = pendingFile{}
		g.pending = g.pending[1:]
	}

	g.pending = nil

	file := filepath.Join(s.opts.Dir, g.id+".mock."+s.format)
	if err := writeAtomic(s.opts.Dir, file, data); err != nil {
		s.failMock(g, fmt.Errorf("recorder: %s: %w", g.id, err))
		return true
	}

	removeOtherFormats(file)

	g.file, g.title = file, name

	if g.failing {
		g.failing, g.err = false, nil
		s.failed.Add(-1)
	}

	if !g.written {
		g.written = true
		s.mocks.Add(1)
	}

	return false
}

// encode returns the mock file of g, and the name it has there: its request and responses decoded afresh, so a
// transform changes this write only, run through the transforms, written by the codec, then in the format of the
// recording.
func (s *RecordSession[X, Q, S]) encode(g *recordGroup) (data []byte, name string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	m := &RecordedMock[Q, S]{ID: g.id, Name: g.name, Responses: make([]S, len(g.responses))}
	if err := json.Unmarshal(g.request, &m.Request); err != nil {
		return nil, "", err
	}

	for i, r := range g.responses {
		if err := json.Unmarshal(r, &m.Responses[i]); err != nil {
			return nil, "", err
		}
	}

	for i, transform := range s.opts.Transforms {
		if err := transform(m); err != nil {
			return nil, "", fmt.Errorf("transform %d: %w", i, err)
		}
	}

	m.ID = g.id

	doc, err := value.ViaJSON(s.codec.Mock(m))
	if err != nil {
		return nil, "", err
	}

	data, err = config.Encode(doc, s.format)

	return data, m.Name, err
}

// writeBody writes a body file. A file of that name holds the same content: it is named after it.
func (s *RecordSession[X, Q, S]) writeBody(p pendingFile) error {
	target := filepath.Join(s.opts.Dir, p.name)

	if _, err := os.Stat(target); err == nil {
		if p.temp != "" {
			os.Remove(p.temp)
		}

		return nil
	}

	if p.temp != "" {
		if err := os.Chmod(p.temp, 0o644); err != nil {
			return err
		}

		return os.Rename(p.temp, target)
	}

	return writeAtomic(s.opts.Dir, target, p.data)
}

// dropUnwritten removes the spooled files of mocks that couldn't be written.
func (s *RecordSession[X, Q, S]) dropUnwritten() {
	for _, g := range s.order {
		for _, p := range g.pending {
			if p.temp != "" {
				os.Remove(p.temp)
			}
		}

		g.pending = nil
	}
}

// failMock counts a mock that couldn't be written once, until it is written: then it doesn't count anymore. Stop
// returns the last error of each mock that still fails.
func (s *RecordSession[X, Q, S]) failMock(g *recordGroup, err error) {
	g.err = err

	if g.failing {
		s.logger.Debug().Err(err).Msg("recorder: not recorded, again")
		return
	}

	g.failing = true
	s.failed.Add(1)
	s.logger.Error().Err(err).Msg("recorder: not recorded")
}

// fail counts a call that couldn't be recorded.
func (s *RecordSession[X, Q, S]) fail(err error) {
	s.failed.Add(1)
	s.logger.Error().Err(err).Msg("recorder: not recorded")

	if len(s.errs) < _maxRecordErrors {
		s.errs = append(s.errs, err)
	} else {
		s.more++
	}
}

// logDrop logs dropped calls, at most once per _dropLogInterval.
func (s *RecordSession[X, Q, S]) logDrop() {
	now := time.Now().UnixNano()

	last := s.lastDrop.Load()
	if now-last < int64(_dropLogInterval) || !s.lastDrop.CompareAndSwap(last, now) {
		return
	}

	s.logger.Warn().Int64("dropped", s.dropped.Load()).Msg("recorder: dropping calls: too many wait to be written")
}

func (s *RecordSession[X, Q, S]) result() *RecordResult {
	res := &RecordResult{Status: s.Status()}

	for _, g := range s.order {
		if !g.written {
			continue
		}

		res.Recordings = append(res.Recordings, Recording{
			ID:        g.id,
			Name:      g.title,
			File:      g.file,
			BodyFiles: slices.Clone(g.bodyFiles),
			Responses: len(g.responses),
		})
	}

	return res
}

func (s *RecordSession[X, Q, S]) err() error {
	errs, more := slices.Clone(s.errs), s.more

	for _, g := range s.order {
		switch {
		case !g.failing:
		case len(errs) < _maxRecordErrors:
			errs = append(errs, g.err)
		default:
			more++
		}
	}

	if more > 0 {
		errs = append(errs, fmt.Errorf("recorder: %d more failures", more))
	}

	return errors.Join(errs...)
}

// queuedCall is a call waiting to be recorded, and the bytes it holds.
type queuedCall[X any] struct {
	x    X
	size int64
}

// recordGroup is the calls of one request, which make one mock.
type recordGroup struct {
	id        string
	name      string
	title     string   // the name its mock file has, which a transform may change
	request   []byte   // the request section, as JSON
	responses [][]byte // the response sections, as JSON, in order; one while they are all the same
	repeats   int      // how many times the one response came, while they are all the same
	pending   []pendingFile
	file      string
	bodyFiles []string
	written   bool
	failing   bool  // its last write failed
	err       error // why it failed
	warned    bool  // it has _maxResponses responses
}

// add adds a response, and reports whether the mock file changes, and whether the mock keeps the response: it keeps
// _maxResponses at most. Equal responses stay one, until another comes: then each takes its place in the sequence,
// when the other one fits after them.
func (g *recordGroup) add(response []byte) (changed, kept bool) {
	switch {
	case len(g.responses) == 0:
		g.responses, g.repeats = [][]byte{response}, 1
		return true, true
	case g.repeats > 0 && bytes.Equal(response, g.responses[0]):
		g.repeats++
		return false, true
	case g.repeats >= _maxResponses:
		return false, false
	case g.repeats > 0:
		first := g.responses[0]
		for range g.repeats - 1 {
			g.responses = append(g.responses, first)
		}

		g.repeats = 0
	}

	if len(g.responses) >= _maxResponses {
		return false, false
	}

	g.responses = append(g.responses, response)

	return true, true
}

// keep adds the body files of a response to those to write. A file of a name it has, written or not, holds the same
// content: its copy goes.
func (g *recordGroup) keep(files *RecordFiles) {
	for _, p := range files.pending {
		known := slices.Contains(g.bodyFiles, p.name) || slices.ContainsFunc(g.pending, func(q pendingFile) bool {
			return q.name == p.name
		})

		switch {
		case !known:
			g.pending = append(g.pending, p)
		case p.temp != "":
			os.Remove(p.temp)
		}
	}

	files.pending = nil
}

// discard discards x when it can be: a call the session doesn't record.
func discard[X any](x X) {
	if d, ok := any(x).(interface{ Discard() }); ok {
		d.Discard()
	}
}

// writeAtomic writes data to file through a temporary file of dir, renamed into place: a reader finds the file
// whole, or not at all.
func writeAtomic(dir, file string, data []byte) error {
	f, err := os.CreateTemp(dir, _tempPattern)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(f.Name(), file)
	}

	if err != nil {
		os.Remove(f.Name())
	}

	return err
}

// removeOtherFormats removes the mock files file replaces in other formats, such as a.mock.yaml for a.mock.json: one
// request has one ID, which one mock holds.
func removeOtherFormats(file string) {
	base := strings.TrimSuffix(file, filepath.Ext(file))

	others, _ := filepath.Glob(base + ".*")
	for _, other := range others {
		if _, ok := config.Format(filepath.Ext(other)); ok && other != file {
			os.Remove(other)
		}
	}
}

// removeStaleTemps removes the temporary files a recording that ended unexpectedly left in dir: those older than
// _staleTemp.
func removeStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".rec-") || !strings.HasSuffix(name, ".tmp") {
			continue
		}

		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > _staleTemp {
			os.Remove(filepath.Join(dir, name))
		}
	}
}
