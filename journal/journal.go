// Package journal keeps the requests a mock server serves, of any protocol, whatever their outcome: a changelog of
// JSON lines in segment files, with an index in memory that narrows queries. Queries match with the matchers of mocks,
// such as those of package match. A server embeds a Verifier, next to its vlatte.Base, to verify its requests.
package journal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

const (
	// DefaultMaxEntries is how many entries the journal of a mock server keeps by default, the newest.
	DefaultMaxEntries = 10_000

	// DefaultMaxBodySize is how many bytes of a body the journal of a mock server keeps by default.
	DefaultMaxBodySize = 1 << 20

	_journalLock = "journal.lock"

	// _journalGitignore keeps the files of a journal's directory out of git.
	_journalGitignore = "# vlatte's request journal: the requests a mock server served, their secrets included.\n*\n"
)

var (
	ErrNoJournal = errors.New("journal: the server keeps no journal of requests: turn its journal on")
	ErrClosed    = errors.New("journal: closed")
	ErrInUse     = errors.New("journal: another journal keeps its files in the directory")
	ErrNotFound  = errors.New("journal: request not found")

	// ErrNoRequestPatterns is the error of a verification with request patterns on a server whose protocol has none.
	ErrNoRequestPatterns = errors.New("journal: the server takes no request patterns")
)

// Options configures a [Journal].
type Options struct {
	// Dir is where the journal keeps its files, across restarts. Empty keeps them in a temporary directory, which
	// Close removes.
	Dir string

	// MaxEntries is how many entries the journal keeps, the newest. 0 keeps them all.
	MaxEntries int

	// MaxBodySize is how many bytes of a body an entry keeps. 0 keeps whole bodies.
	MaxBodySize int64

	// Logger logs what the journal fails to keep. Nil logs nothing.
	Logger *zerolog.Logger
}

// Journal keeps the requests a mock server serves, of any protocol, in files: a log of entries, one per line, in
// segments the journal starts as one fills. It indexes them in memory, so a query reads from disk only the entries
// its indexed fields leave, and only when it selects by their headers or bodies, or returns them.
//
// Append is synchronous: a request is in the journal once its answer is served, for a test to check right after.
// A journal is safe for concurrent use. Queries read without holding the journal: appends don't wait on them.
type Journal struct {
	dir        string
	temp       bool
	maxEntries int
	maxBody    int64
	perSegment int
	logger     *zerolog.Logger
	lock       *os.File // nil where directories can't be locked

	mu       sync.Mutex
	closed   bool
	seq      uint64 // the last sequence number given
	files    []int  // the segments on disk, in order: the last is the active one
	active   *os.File
	size     int64 // bytes of the active segment
	count    int   // entries of the active segment
	rows     []journalRow
	index    journalIndex
	failures int
}

// New opens the journal of o.Dir, with the entries it holds, or creates it. One journal at a time keeps its
// files in a directory: another one fails with ErrInUse, where the system locks files.
func New(o Options) (*Journal, error) {
	switch {
	case o.MaxEntries < 0:
		return nil, fmt.Errorf("journal: MaxEntries must be 0 or more, not %d", o.MaxEntries)
	case o.MaxBodySize < 0:
		return nil, fmt.Errorf("journal: MaxBodySize must be 0 or more, not %d", o.MaxBodySize)
	}

	j := &Journal{
		dir:        o.Dir,
		maxEntries: o.MaxEntries,
		maxBody:    o.MaxBodySize,
		perSegment: _segmentEntries,
		logger:     o.Logger,
		index:      newJournalIndex(),
	}

	// A segment holds a fourth of the entries kept at most: dropping the oldest leaves the most on disk.
	if o.MaxEntries > 0 {
		j.perSegment = min(_segmentEntries, max(1, o.MaxEntries/4))
	}

	if j.logger == nil {
		j.logger = new(zerolog.Nop())
	}

	if err := j.openDir(); err != nil {
		return nil, err
	}

	if err := j.load(); err != nil {
		j.release()
		return nil, err
	}

	return j, nil
}

// MustNew is like New, but panics on an error.
func MustNew(o Options) *Journal {
	j, err := New(o)
	if err != nil {
		panic(err)
	}

	return j
}

// Dir returns the directory of the journal's files, as an absolute path.
func (j *Journal) Dir() string {
	if j == nil {
		return ""
	}

	return j.dir
}

// Append adds e to the journal, and gives it its sequence number. A body over the journal's limit loses the rest,
// and text that isn't UTF-8, which JSON can't hold, gets U+FFFD in place of its bad bytes. A failure is logged, the
// first and then each time their count doubles, and returned.
func (j *Journal) Append(e *Entry) error {
	if j == nil {
		return ErrNoJournal
	}

	if j.maxBody > 0 && int64(len(e.Body)) > j.maxBody {
		e.Body = ClipBody(e.Body, int(j.maxBody))
		e.BodyTruncated = true
	}

	e.validUTF8()

	line, err := encodeEntry(e)

	sealed, err := j.append(e, line, err)

	// A full segment gets its sidecar once the lock is released: appends don't wait on it.
	if sealed != nil {
		sealed.finish(j.logger)
	}

	return err
}

// Get returns the entry of the request id.
func (j *Journal) Get(id string) (*Entry, error) {
	if j == nil {
		return nil, ErrNoJournal
	}

	j.mu.Lock()

	if j.closed {
		j.mu.Unlock()
		return nil, ErrClosed
	}

	var (
		row   journalRow
		found bool
	)

	if seq, ok := j.index.ids[id]; ok {
		var i int
		if i, found = searchRows(j.rows, seq); found {
			row = j.rows[i]
		}
	}

	j.mu.Unlock()

	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	files := segmentFiles{dir: j.dir}
	defer files.close()

	e, err := files.read(row)
	if errors.Is(err, fs.ErrNotExist) {
		// The journal dropped it meanwhile.
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return e, err
}

// Find returns the entries q selects, with filters, the newest first, a page of them when q has a Limit.
func (j *Journal) Find(ctx context.Context, q Query, filters ...Filter) (Page, error) {
	return j.query(ctx, &q, filters, true)
}

// Count returns how many entries q selects, with filters. It reads no entry when q selects by indexed fields only.
func (j *Journal) Count(ctx context.Context, q Query, filters ...Filter) (int, error) {
	page, err := j.query(ctx, &q, filters, false)
	return page.Count, err
}

// Clear removes every entry. Sequence numbers go on from the last one given while the journal is open: a journal
// opened on an empty directory starts them over. A file it can't remove yet, such as one a query holds open where the
// system keeps it, it logs and removes later: its entries are gone either way.
func (j *Journal) Clear() error {
	if j == nil {
		return ErrNoJournal
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return ErrClosed
	}

	next := j.files[len(j.files)-1] + 1

	f, err := openSegment(j.dir, next)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}

	// The tombstone comes first, and stays: a segment that stays on disk, as a crash or a removal that fails leaves
	// one, is dropped by the next journal of the directory, which removes the tombstone once none is left.
	if err := writeCleared(j.dir, next); err != nil {
		f.Close()
		removeSegment(j.dir, next)

		return fmt.Errorf("journal: %w", err)
	}

	j.active.Close()

	// The journal keeps a segment that stays on disk: the next trim removes it.
	var left []int

	for _, n := range j.files {
		if err := removeSegment(j.dir, n); err != nil {
			j.logger.Warn().Err(err).Msg("journal: removing a segment")

			left = append(left, n)
		}
	}

	j.files = append(left, next)
	j.active, j.size, j.count = f, 0, 0
	j.rows = nil
	j.index = newJournalIndex()

	return nil
}

// Close closes the journal, and removes its directory if it is a temporary one. Closing it again does nothing.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil
	}

	j.closed = true

	active := j.files[len(j.files)-1]

	// An empty segment holds nothing to keep. Another gets its sidecar, for the next journal to open it fast.
	var err error
	if j.count == 0 {
		err = j.active.Close()
		removeSegment(j.dir, active)
	} else {
		err = j.seal(active, j.active).finish(j.logger)
	}

	j.release()

	return err
}

// append writes line, the line of e, or fails with encodeErr, the error encoding it, under the lock. It returns the
// segment it filled, if it did, for its caller to finish.
func (j *Journal) append(e *Entry, line []byte, encodeErr error) (*sealedSegment, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil, ErrClosed
	}

	if encodeErr != nil {
		return nil, j.failed(fmt.Errorf("journal: %w", encodeErr))
	}

	seq := j.seq + 1
	data := withSeq(line, seq)

	if _, err := j.active.Write(data); err != nil {
		err = j.failed(fmt.Errorf("journal: %w", err))

		// A line cut short would run into the next one: the segment loses it, or the journal starts a new one. When
		// neither works, the next line starts after it, as the file has it, on a line of its own if it can.
		if j.active.Truncate(j.size) != nil {
			if sealed := j.rotate(); sealed != nil {
				return sealed, err
			}

			j.resync()
		}

		return nil, err
	}

	j.seq = seq
	e.Seq = seq

	j.rows = append(j.rows, journalRow{
		Seq:     seq,
		ID:      e.ID,
		Time:    e.Time,
		Kind:    e.Kind,
		Outcome: e.Outcome,
		MockID:  e.MockID,
		Method:  e.Method,
		Path:    e.Path,
		Target:  e.Target,
		Status:  e.Status,
		Segment: j.files[len(j.files)-1],
		Offset:  j.size,
		Size:    len(data),
	})
	j.index.add(&j.rows[len(j.rows)-1])

	j.size += int64(len(data))
	j.count++

	j.trim()

	if j.count >= j.perSegment || j.size >= _segmentBytes {
		return j.rotate(), nil
	}

	return nil, nil
}

// openDir creates the journal's directory, or a temporary one, and locks it.
func (j *Journal) openDir() error {
	if j.dir == "" {
		dir, err := os.MkdirTemp("", "vlatte-journal-*")
		if err != nil {
			return fmt.Errorf("journal: %w", err)
		}

		j.dir, j.temp = dir, true
	} else {
		// The files are found where the journal opened, whatever the working directory becomes.
		dir, err := filepath.Abs(j.dir)
		if err != nil {
			return fmt.Errorf("journal: %w", err)
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("journal: %w", err)
		}

		j.dir = dir
		j.ignoreDir()
	}

	lock, err := lockDir(j.dir)
	if err != nil {
		j.release()
		return err
	}

	j.lock = lock

	return nil
}

// ignoreDir writes a .gitignore of every file into the journal's directory, unless it has one: the journal holds
// requests, their secrets included, which a project that commits its testdata must not commit.
func (j *Journal) ignoreDir() {
	path := filepath.Join(j.dir, ".gitignore")

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return
	}

	if err == nil {
		_, err = f.WriteString(_journalGitignore)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}

	if err != nil {
		j.logger.Warn().Err(err).Msg("journal: writing the .gitignore of its directory")
	}
}

// load reads the entries of the segments on disk, from their sidecars where they describe them, drops those past
// MaxEntries, and starts a new segment to append to: a segment that a crash cut short is never written again.
func (j *Journal) load() error {
	numbers, sidecars, err := listSegments(j.dir)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}

	// The segments a Clear removed, which stayed on disk, go now.
	cleared := readCleared(j.dir)
	if cleared > 0 {
		numbers = j.dropCleared(numbers, cleared)
	}

	// A locked directory is this journal's alone: a file being written is one a crash left.
	if j.lock != nil {
		if temps, err := filepath.Glob(filepath.Join(j.dir, _journalTemps)); err == nil {
			for _, temp := range temps {
				os.Remove(temp)
			}
		}
	}

	// A sidecar a failed removal left describes no log.
	for _, n := range sidecars {
		if !slices.Contains(numbers, n) {
			os.Remove(indexPath(j.dir, n))
		}
	}

	scanned := make(map[int][]journalRow)

	for _, n := range numbers {
		rows, ok := readIndex(j.dir, n)
		if !ok {
			if rows, err = j.scan(n); err != nil {
				return err
			}

			scanned[n] = rows
		}

		for i := range rows {
			// Sequence numbers only grow: a row out of order is none of this journal's.
			if rows[i].Seq <= j.seq {
				continue
			}

			j.rows = append(j.rows, rows[i])
			j.index.add(&j.rows[len(j.rows)-1])
			j.seq = rows[i].Seq
		}
	}

	// The new segment comes after those a tombstone drops, which may be on disk still.
	next := max(1, cleared)
	if len(numbers) > 0 {
		next = max(next, numbers[len(numbers)-1]+1)
	}

	f, err := openSegment(j.dir, next)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}

	j.active = f
	j.files = append(numbers, next)
	j.trim()
	j.index.compact(j.first())

	// The segments it scanned and keeps get their sidecars: the next journal reads those.
	for n, rows := range scanned {
		if slices.Contains(j.files, n) {
			if err := writeIndex(j.dir, n, rows); err != nil {
				j.logger.Warn().Err(err).Msg("journal: writing the index of a segment")
			}
		}
	}

	return nil
}

// dropCleared removes the segments of numbers before cleared, those a Clear removed, and returns the others. Once
// none is left, the tombstone goes too.
func (j *Journal) dropCleared(numbers []int, cleared int) []int {
	kept := numbers[:0]
	left := false

	for _, n := range numbers {
		if n >= cleared {
			kept = append(kept, n)
			continue
		}

		// The next journal of the directory tries again.
		if err := removeSegment(j.dir, n); err != nil {
			j.logger.Warn().Err(err).Msg("journal: removing a segment a Clear removed")

			left = true
		}
	}

	if !left {
		os.Remove(filepath.Join(j.dir, _journalCleared))
	}

	return kept
}

// scan reads the rows of segment n from its log, and logs the lines it left out.
func (j *Journal) scan(n int) ([]journalRow, error) {
	rows, bad, err := scanSegment(j.dir, n)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}

	if bad > 0 {
		j.logger.Warn().Str("file", segmentPath(j.dir, n)).Int("lines", bad).Msg("journal: left out lines that don't decode")
	}

	return rows, nil
}

// seal returns segment n, whose log is f, to finish: the rows the journal holds of it are all those of its log, as
// it holds no fewer than a segment's. A temporary journal writes no sidecar: nothing reads it.
func (j *Journal) seal(n int, f *os.File) *sealedSegment {
	s := &sealedSegment{dir: j.dir, n: n, file: f}

	if !j.temp {
		i := len(j.rows)
		for i > 0 && j.rows[i-1].Segment == n {
			i--
		}

		s.rows, s.index = j.rows[i:], true
	}

	return s
}

// trim drops the oldest entries past MaxEntries, and the segments that hold none of the entries left.
func (j *Journal) trim() {
	if j.maxEntries > 0 && len(j.rows) > j.maxEntries {
		dropped := len(j.rows) - j.maxEntries
		j.index.drop(j.rows[:dropped])

		// A query reads the rows it was given without the lock: they are never changed, only left behind.
		j.rows = j.rows[dropped:]
	}

	active := j.files[len(j.files)-1]

	oldest := active
	if len(j.rows) > 0 {
		oldest = j.rows[0].Segment
	}

	kept := j.files[:0]

	for _, n := range j.files {
		if n >= oldest || n == active {
			kept = append(kept, n)
			continue
		}

		if err := removeSegment(j.dir, n); err != nil {
			// The next trim tries again.
			j.logger.Warn().Err(err).Msg("journal: removing a segment")

			kept = append(kept, n)
		}
	}

	j.files = kept
}

// rotate starts a new segment, and returns the full one to finish once the lock is released. On a failure, the
// journal keeps appending to the full one.
func (j *Journal) rotate() *sealedSegment {
	next := j.files[len(j.files)-1] + 1

	f, err := openSegment(j.dir, next)
	if err != nil {
		j.logger.Warn().Err(err).Msg("journal: starting a new segment")
		return nil
	}

	sealed := j.seal(j.files[len(j.files)-1], j.active)

	j.active, j.size, j.count = f, 0, 0
	j.files = append(j.files, next)
	j.index.compact(j.first())

	return sealed
}

// resync takes the size of the active segment from its file, after a write that left part of a line in it, ended by
// a new line when the file takes one: later rows have the offsets their lines have.
func (j *Journal) resync() {
	if _, err := j.active.Write([]byte{'\n'}); err != nil {
		j.logger.Warn().Err(err).Msg("journal: ending a line cut short")
	}

	if info, err := j.active.Stat(); err == nil {
		j.size = info.Size()
	}
}

// first returns the sequence number of the oldest entry the journal holds, or the next one it gives.
func (j *Journal) first() uint64 {
	if len(j.rows) > 0 {
		return j.rows[0].Seq
	}

	return j.seq + 1
}

// failed counts err, and logs it when the count is a power of 2: a journal that can't write logs the first failures,
// then fewer and fewer.
func (j *Journal) failed(err error) error {
	j.failures++

	if j.failures&(j.failures-1) == 0 {
		j.logger.Error().Err(err).Int("failures", j.failures).Msg("journal: an entry was not kept")
	}

	return err
}

// release unlocks the journal's directory, and removes it if it is a temporary one.
func (j *Journal) release() {
	if j.lock != nil {
		j.lock.Close()
	}

	if j.temp {
		os.RemoveAll(j.dir)
	}
}

// query runs q, with filters: it counts the entries q selects and, with collect, returns the page of them q asks for.
func (j *Journal) query(ctx context.Context, q *Query, filters []Filter, collect bool) (Page, error) {
	if j == nil {
		return Page{}, ErrNoJournal
	}

	if err := q.check(); err != nil {
		return Page{}, err
	}

	snap, err := j.snapshot(q)
	if err != nil {
		return Page{}, err
	}

	accepted, err := snap.accept(ctx, q)
	if err != nil {
		return Page{}, err
	}

	rows := snap.candidates(j.lists(q, accepted))

	files := segmentFiles{dir: snap.dir}
	defer files.close()

	var (
		page Page
		disk = q.readsEntries(filters)
	)

	for i := len(rows) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}

		row := &rows[i]

		ok, err := q.keeps(ctx, row)
		if err != nil {
			return Page{}, err
		}

		if !ok {
			continue
		}

		paged := collect && (q.Before == 0 || row.Seq < q.Before)
		full := q.Limit > 0 && len(page.Entries) >= q.Limit

		var entry *Entry

		if disk || (paged && !full) {
			entry, err = files.read(*row)
			if errors.Is(err, fs.ErrNotExist) {
				// The journal dropped it while the query ran.
				continue
			}

			if err != nil {
				return Page{}, fmt.Errorf("journal: reading entry %d: %w", row.Seq, err)
			}
		}

		if disk {
			if ok, err := q.selects(ctx, entry, filters); err != nil || !ok {
				if err != nil {
					return Page{}, err
				}

				continue
			}
		}

		page.Count++

		switch {
		case !paged:
		case full:
			if page.Next == 0 {
				page.Next = page.Entries[len(page.Entries)-1].Seq
			}
		default:
			page.Entries = append(page.Entries, entry)
		}
	}

	return page, nil
}

// snapshot copies what q reads of the journal, for q to read once the lock is released: the rows, and the values of
// the indexed fields q matches.
func (j *Journal) snapshot(q *Query) (*journalSnapshot, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil, ErrClosed
	}

	s := &journalSnapshot{dir: j.dir, rows: slices.Clip(j.rows)}

	if q.MockID != nil {
		s.mockIDs = j.index.mockID.values()
	}

	if q.Method != nil {
		s.methods = j.index.method.values()
	}

	if q.Path != nil {
		s.paths = j.index.path.values()
	}

	if q.Status != nil {
		s.statuses = j.index.status.values()
	}

	return s, nil
}

// lists returns, for each indexed field q selects by, the lists of the values it accepts: those the journal holds
// now, which may name entries its snapshot's rows don't, newer ones, or miss some, which it dropped meanwhile.
func (j *Journal) lists(q *Query, a *journalAccepted) [][][]uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()

	var fields [][][]uint64

	if q.Kind != "" {
		fields = append(fields, j.index.kind.of([]vlatte.Kind{q.Kind}))
	}

	if q.Outcome != "" {
		fields = append(fields, j.index.outcome.of([]Outcome{q.Outcome}))
	}

	if q.MockID != nil {
		fields = append(fields, j.index.mockID.of(a.mockIDs))
	}

	if q.Method != nil {
		fields = append(fields, j.index.method.of(a.methods))
	}

	if q.Path != nil {
		fields = append(fields, j.index.path.of(a.paths))
	}

	if q.Status != nil {
		fields = append(fields, j.index.status.of(a.statuses))
	}

	return fields
}

// sealedSegment is a segment the journal no longer appends to: its log to close, and, with index, its sidecar to
// write with rows. It touches nothing the journal's lock guards.
type sealedSegment struct {
	dir   string
	n     int
	file  *os.File
	rows  []journalRow
	index bool
}

// finish closes the log of s and writes its sidecar. The log is on disk before its sidecar describes it: a crash
// leaves no sidecar of a log that lost its rows.
func (s *sealedSegment) finish(logger *zerolog.Logger) error {
	if !s.index {
		return s.file.Close()
	}

	err := s.file.Sync()
	if closeErr := s.file.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = writeIndex(s.dir, s.n, s.rows)
	}

	if err != nil {
		logger.Warn().Err(err).Msg("journal: writing the index of a segment")
	}

	return err
}

// segmentFiles opens the segments a query reads, once each.
type segmentFiles struct {
	dir   string
	files map[int]*os.File
}

func (s *segmentFiles) read(row journalRow) (*Entry, error) {
	f, ok := s.files[row.Segment]
	if !ok {
		var err error
		if f, err = os.Open(segmentPath(s.dir, row.Segment)); err != nil {
			return nil, err
		}

		if s.files == nil {
			s.files = make(map[int]*os.File)
		}

		s.files[row.Segment] = f
	}

	return readEntry(f, row)
}

func (s *segmentFiles) close() {
	for _, f := range s.files {
		f.Close()
	}
}

// ClipBody returns the first n bytes of body, n less than its length, fewer when n cuts a character of a body whose
// first n bytes are text: clipped text stays text, as a journal keeps it.
func ClipBody(body []byte, n int) []byte {
	cut := n
	for i := 1; i < utf8.UTFMax && cut > 0 && !utf8.RuneStart(body[cut]); i++ {
		cut--
	}

	if cut == n {
		return body[:n]
	}

	// The character that starts at cut is one n cuts, if body is text.
	if r, size := utf8.DecodeRune(body[cut:]); r == utf8.RuneError || cut+size <= n || !utf8.Valid(body[:cut]) {
		return body[:n]
	}

	return body[:cut]
}

// openSegment creates segment n of dir, to append to. Its entries are requests, which may hold secrets: only the
// user reads them.
func openSegment(dir string, n int) (*os.File, error) {
	// A sidecar a failed removal left would describe another log.
	os.Remove(indexPath(dir, n))

	return os.OpenFile(segmentPath(dir, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
}
