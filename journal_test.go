package vlatte_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var _t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func newJournal(t *testing.T, o vlatte.JournalOptions) *vlatte.Journal {
	t.Helper()

	if o.Dir == "" {
		o.Dir = t.TempDir()
	}

	j, err := vlatte.NewJournal(o)
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	return j
}

func entry(id string, mutate ...func(e *vlatte.JournalEntry)) *vlatte.JournalEntry {
	e := &vlatte.JournalEntry{
		ID:      id,
		Time:    _t0,
		Kind:    "http",
		Outcome: vlatte.OutcomeMatched,
		Method:  "GET",
		Path:    "/" + id,
		Target:  "http://127.0.0.1/" + id,
		Status:  200,
	}

	for _, m := range mutate {
		m(e)
	}

	return e
}

func entryIDs(page vlatte.JournalPage) []string {
	out := make([]string, 0, len(page.Entries))
	for _, e := range page.Entries {
		out = append(out, e.ID)
	}

	return out
}

func find(t *testing.T, j *vlatte.Journal, q vlatte.JournalQuery, filters ...vlatte.EntryFilter) []string {
	t.Helper()

	page, err := j.Find(t.Context(), q, filters...)
	require.NoError(t, err)

	count, err := j.Count(t.Context(), q, filters...)
	require.NoError(t, err)
	require.Equal(t, page.Count, count, "Count and Find agree")

	if q.Limit == 0 && q.Before == 0 {
		require.Len(t, page.Entries, page.Count)
	}

	return entryIDs(page)
}

func TestJournal_SelectsByEachField(t *testing.T) {
	// A test verifies a request by any of the fields every protocol has: each field must narrow the entries, and
	// the entries come newest first, as the latest request is the one a test most often checks.
	j := newJournal(t, vlatte.JournalOptions{})

	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, j.Append(entry("b", func(e *vlatte.JournalEntry) {
		e.Method, e.Status, e.MockID = "POST", 201, "m1"
		e.Header = map[string][]string{"Content-Type": {"application/json"}}
		e.Body = []byte(`{"id":7}`)
	})))
	require.NoError(t, j.Append(entry("c", func(e *vlatte.JournalEntry) {
		e.Kind, e.Outcome, e.Status, e.Method = "grpc", vlatte.OutcomeUnmatched, 5, ""
		e.Path, e.Target = "/pkg.Svc/Hi", "/pkg.Svc/Hi"
		e.Header = map[string][]string{"x-team": {"core"}}
	})))
	require.NoError(t, j.Append(entry("d", func(e *vlatte.JournalEntry) {
		e.Outcome, e.Status, e.Time = vlatte.OutcomeFailed, 418, _t0.Add(time.Hour)
	})))

	tests := []struct {
		name string
		give vlatte.JournalQuery
		want []string
	}{
		{name: "all, newest first", give: vlatte.JournalQuery{}, want: []string{"d", "c", "b", "a"}},
		{name: "kind", give: vlatte.JournalQuery{Kind: "grpc"}, want: []string{"c"}},
		{name: "unknown kind", give: vlatte.JournalQuery{Kind: "ws"}, want: []string{}},
		{name: "outcome", give: vlatte.JournalQuery{Outcome: vlatte.OutcomeFailed}, want: []string{"d"}},
		{name: "mock id", give: vlatte.JournalQuery{MockID: match.Equal("m1")}, want: []string{"b"}},
		{name: "method", give: vlatte.JournalQuery{Method: match.EqualIgnoreCase("get")}, want: []string{"d", "a"}},
		{name: "path glob", give: vlatte.JournalQuery{Path: match.GlobMatch("/pkg.*")}, want: []string{"c"}},
		{name: "target", give: vlatte.JournalQuery{Target: match.Contain("/b")}, want: []string{"b"}},
		{name: "status as a number", give: vlatte.JournalQuery{Status: match.GreaterThan(200)}, want: []string{"d", "b"}},
		{name: "since", give: vlatte.JournalQuery{Since: _t0.Add(time.Minute)}, want: []string{"d"}},
		{name: "until", give: vlatte.JournalQuery{Until: _t0.Add(time.Hour)}, want: []string{"c", "b", "a"}},
		{name: "header in any case", give: vlatte.JournalQuery{Header: map[string]vlatte.Matcher{"content-type": match.Contain("json")}}, want: []string{"b"}},
		{name: "metadata", give: vlatte.JournalQuery{Header: map[string]vlatte.Matcher{"X-Team": match.Equal("core")}}, want: []string{"c"}},
		{name: "absent header is empty", give: vlatte.JournalQuery{Header: map[string]vlatte.Matcher{"X-None": match.Empty()}}, want: []string{"d", "c", "b", "a"}},
		{name: "json body", give: vlatte.JournalQuery{Body: match.JSONPath("$.id", match.Equal(7))}, want: []string{"b"}},
		{
			name: "fields intersect",
			give: vlatte.JournalQuery{Kind: "http", Method: match.Equal("GET"), Status: match.Equal(200)},
			want: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, find(t, j, tt.give))
		})
	}
}

func TestJournal_FiltersRunOnEntriesTheIndexLeft(t *testing.T) {
	// A protocol's request pattern is a filter: it only sees the entries the other fields selected, so a pattern,
	// which rebuilds a request, runs as little as it can.
	j := newJournal(t, vlatte.JournalOptions{})

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, j.Append(entry(id)))
	}

	var seen []string

	got := find(t, j, vlatte.JournalQuery{Path: match.Equal("/b")}, func(_ context.Context, e *vlatte.JournalEntry) (bool, error) {
		seen = append(seen, e.ID)
		return true, nil
	})

	require.Equal(t, []string{"b"}, got)
	require.Equal(t, []string{"b", "b"}, seen, "once for Find, once for Count")

	failing := func(context.Context, *vlatte.JournalEntry) (bool, error) { return false, errors.New("bad pattern") }
	_, err := j.Find(t.Context(), vlatte.JournalQuery{}, failing)
	require.ErrorContains(t, err, "bad pattern")
}

func TestJournal_PagesKeepTheirCount(t *testing.T) {
	// Count is the number of entries a query selects whatever page it asks for: an admin client verifies a count
	// without paging, and pages through the entries without the count moving.
	j := newJournal(t, vlatte.JournalOptions{})

	for i := range 7 {
		require.NoError(t, j.Append(entry(strconv.Itoa(i))))
	}

	var pages [][]string

	q := vlatte.JournalQuery{Limit: 3}
	for {
		page, err := j.Find(t.Context(), q)
		require.NoError(t, err)
		require.Equal(t, 7, page.Count)

		pages = append(pages, entryIDs(page))

		if page.Next == 0 {
			break
		}

		q.Before = page.Next
	}

	require.Equal(t, [][]string{{"6", "5", "4"}, {"3", "2", "1"}, {"0"}}, pages)
}

func TestJournal_RejectsBadQueries(t *testing.T) {
	j := newJournal(t, vlatte.JournalOptions{})

	_, err := j.Find(t.Context(), vlatte.JournalQuery{Limit: -1})
	require.ErrorContains(t, err, "limit")

	_, err = j.Find(t.Context(), vlatte.JournalQuery{Outcome: "won"})
	require.ErrorContains(t, err, `outcome "won"`)
}

func TestJournal_AMatcherThatPanicsFailsTheQuery(t *testing.T) {
	// A user's matcher runs in a test or an admin request: its panic is an error of the query, not a crash.
	j := newJournal(t, vlatte.JournalOptions{})
	require.NoError(t, j.Append(entry("a")))

	boom := vlatte.MatcherFunc("boom", func(vlatte.MatcherArg) (bool, error) { panic("boom") })

	for _, q := range []vlatte.JournalQuery{{Path: boom}, {Target: boom}, {Body: boom}} {
		_, err := j.Find(t.Context(), q)
		require.ErrorIs(t, err, vlatte.ErrMatchPanicked)
	}
}

func TestJournal_GetsAnEntryByID(t *testing.T) {
	j := newJournal(t, vlatte.JournalOptions{})

	want := entry("req-1", func(e *vlatte.JournalEntry) {
		e.Header = map[string][]string{"Accept": {"*/*", "text/plain"}}
		e.Body = []byte("hello")
		e.Elapsed = 1500 * time.Microsecond
		e.Message = "why"
		e.NearMisses = []vlatte.NearMiss{{MockID: "m", Passed: 1, Total: 2, Failed: []string{"method"}}}
	})
	require.NoError(t, j.Append(want))
	require.Equal(t, uint64(1), want.Seq)

	got, err := j.Get("req-1")
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = j.Get("nope")
	require.ErrorIs(t, err, vlatte.ErrRequestNotFound)
}

func TestJournal_KeepsBodiesAsTextWhenTheyAreText(t *testing.T) {
	// The files are a log a person reads: a text body stays text. Another body still comes back whole.
	j := newJournal(t, vlatte.JournalOptions{})

	binary := []byte{0xff, 0x00, 0xfe}
	require.NoError(t, j.Append(entry("text", func(e *vlatte.JournalEntry) { e.Body = []byte("héllo\nworld") })))
	require.NoError(t, j.Append(entry("binary", func(e *vlatte.JournalEntry) { e.Body = binary })))

	data, err := os.ReadFile(filepath.Join(j.Dir(), "journal-000001.log"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2, "one line per entry")
	require.Contains(t, lines[0], `"body":"héllo\nworld"`)
	require.Contains(t, lines[1], `"body_base64":"/wD+"`)
	require.True(t, strings.HasPrefix(lines[1], `{"seq":2,"id":"binary"`), lines[1])

	got, err := j.Get("binary")
	require.NoError(t, err)
	require.Equal(t, binary, got.Body)
}

func TestJournal_ClipsBodiesOverTheLimit(t *testing.T) {
	j := newJournal(t, vlatte.JournalOptions{MaxBodySize: 4})

	require.NoError(t, j.Append(entry("a", func(e *vlatte.JournalEntry) { e.Body = []byte("123456") })))
	require.NoError(t, j.Append(entry("b", func(e *vlatte.JournalEntry) { e.Body = []byte("1234") })))

	a, err := j.Get("a")
	require.NoError(t, err)
	require.Equal(t, "1234", string(a.Body))
	require.True(t, a.BodyTruncated)

	b, err := j.Get("b")
	require.NoError(t, err)
	require.False(t, b.BodyTruncated)
}

func TestJournal_KeepsTheNewestEntries(t *testing.T) {
	// A server that runs for long keeps MaxEntries exactly, and the files of the entries it dropped go: the
	// journal's disk use is bounded.
	j := newJournal(t, vlatte.JournalOptions{MaxEntries: 8})

	for i := range 50 {
		require.NoError(t, j.Append(entry(strconv.Itoa(i))))
	}

	page, err := j.Find(t.Context(), vlatte.JournalQuery{})
	require.NoError(t, err)
	require.Equal(t, []string{"49", "48", "47", "46", "45", "44", "43", "42"}, entryIDs(page))

	_, err = j.Get("41")
	require.ErrorIs(t, err, vlatte.ErrRequestNotFound)

	require.Empty(t, find(t, j, vlatte.JournalQuery{Path: match.RegExpr("^/4[01]$")}), "dropped entries leave the index")

	logs, err := filepath.Glob(filepath.Join(j.Dir(), "journal-*.log"))
	require.NoError(t, err)
	require.LessOrEqual(t, len(logs), 5, "segments of 2 entries: 8 kept, the active one, and one partly dropped")
}

func TestJournal_ClearsEverything(t *testing.T) {
	j := newJournal(t, vlatte.JournalOptions{})

	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, j.Clear())

	require.Empty(t, find(t, j, vlatte.JournalQuery{}))
	require.Empty(t, find(t, j, vlatte.JournalQuery{Path: match.Anything()}))

	_, err := j.Get("a")
	require.ErrorIs(t, err, vlatte.ErrRequestNotFound)

	e := entry("b")
	require.NoError(t, j.Append(e))
	require.Equal(t, uint64(2), e.Seq, "sequence numbers go on: a cursor never names a new entry")
	require.Equal(t, []string{"b"}, find(t, j, vlatte.JournalQuery{}))

	logs, err := filepath.Glob(filepath.Join(j.Dir(), "journal-*.log"))
	require.NoError(t, err)
	require.Len(t, logs, 1)
}

func TestJournal_KeepsItsEntriesAcrossRestarts(t *testing.T) {
	// A journal of a named directory is a changelog: a server that restarts finds the requests it served before,
	// and numbers new ones after them.
	dir := t.TempDir()

	j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.NoError(t, err)
	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, j.Append(entry("b", func(e *vlatte.JournalEntry) { e.Body = []byte("x") })))
	require.NoError(t, j.Close())

	j = newJournal(t, vlatte.JournalOptions{Dir: dir})
	require.Equal(t, []string{"b", "a"}, find(t, j, vlatte.JournalQuery{}))
	require.Equal(t, []string{"b"}, find(t, j, vlatte.JournalQuery{Body: match.Equal("x")}))

	e := entry("c")
	require.NoError(t, j.Append(e))
	require.Equal(t, uint64(3), e.Seq)
	require.Equal(t, []string{"c", "b", "a"}, find(t, j, vlatte.JournalQuery{Kind: "http"}))
}

func TestJournal_RecoversFromLinesACrashCutShort(t *testing.T) {
	// A crash may cut the last line short, or leave a line that doesn't decode: the journal reads the others,
	// logs what it left out, and appends after them.
	dir := t.TempDir()

	j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.NoError(t, err)
	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, j.Append(entry("b")))
	require.NoError(t, j.Close())

	path := filepath.Join(dir, "journal-000001.log")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	first, rest, _ := bytes.Cut(data, []byte("\n"))
	corrupt := append(append(append([]byte{}, first...), "\nnot json\n"...), rest...)
	corrupt = append(corrupt, `{"seq":3,"id":"cut`...)
	require.NoError(t, os.WriteFile(path, corrupt, 0o600))

	var logs bytes.Buffer
	logger := zerolog.New(&logs)

	j = newJournal(t, vlatte.JournalOptions{Dir: dir, Logger: &logger})
	require.Equal(t, []string{"b", "a"}, find(t, j, vlatte.JournalQuery{}))
	require.Contains(t, logs.String(), `"lines":2`)

	e := entry("c")
	require.NoError(t, j.Append(e))
	require.Equal(t, uint64(3), e.Seq)

	got, err := j.Get("c")
	require.NoError(t, err)
	require.Equal(t, "c", got.ID)
}

func TestJournal_TemporaryDirectoryGoesOnClose(t *testing.T) {
	j, err := vlatte.NewJournal(vlatte.JournalOptions{})
	require.NoError(t, err)
	require.NoError(t, j.Append(entry("a")))

	dir := j.Dir()
	require.DirExists(t, dir)

	require.NoError(t, j.Close())
	require.NoError(t, j.Close(), "closing again does nothing")
	require.NoDirExists(t, dir)
}

func TestJournal_ClosedOrMissingFailsLoudly(t *testing.T) {
	// A server without a journal, or one closed, answers each call with an error a test reports, never silence.
	var none *vlatte.Journal

	require.ErrorIs(t, none.Append(entry("a")), vlatte.ErrNoJournal)
	_, err := none.Find(t.Context(), vlatte.JournalQuery{})
	require.ErrorIs(t, err, vlatte.ErrNoJournal)
	_, err = none.Count(t.Context(), vlatte.JournalQuery{})
	require.ErrorIs(t, err, vlatte.ErrNoJournal)
	_, err = none.Get("a")
	require.ErrorIs(t, err, vlatte.ErrNoJournal)
	require.ErrorIs(t, none.Clear(), vlatte.ErrNoJournal)
	require.NoError(t, none.Close())
	require.Empty(t, none.Dir())

	j := newJournal(t, vlatte.JournalOptions{})
	require.NoError(t, j.Close())

	require.ErrorIs(t, j.Append(entry("a")), vlatte.ErrJournalClosed)
	_, err = j.Find(t.Context(), vlatte.JournalQuery{})
	require.ErrorIs(t, err, vlatte.ErrJournalClosed)
	_, err = j.Get("a")
	require.ErrorIs(t, err, vlatte.ErrJournalClosed)
	require.ErrorIs(t, j.Clear(), vlatte.ErrJournalClosed)
}

func TestJournal_OneJournalPerDirectory(t *testing.T) {
	// Two journals appending to one directory would mix their files: the second fails, until the first closes.
	if runtime.GOOS == "windows" {
		t.Skip("directories aren't locked on Windows")
	}

	dir := t.TempDir()

	j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.NoError(t, err)

	_, err = vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.ErrorIs(t, err, vlatte.ErrJournalInUse)

	require.NoError(t, j.Close())

	j, err = vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.NoError(t, err)
	require.NoError(t, j.Close())
}

func TestJournal_RejectsBadOptions(t *testing.T) {
	_, err := vlatte.NewJournal(vlatte.JournalOptions{MaxEntries: -1})
	require.ErrorContains(t, err, "MaxEntries")

	_, err = vlatte.NewJournal(vlatte.JournalOptions{MaxBodySize: -1})
	require.ErrorContains(t, err, "MaxBodySize")
}

func TestJournal_IsSafeForConcurrentUse(t *testing.T) {
	// Requests append while tests and admin clients query, and reset clears: run under -race, nothing may race,
	// and every entry a query returns must be whole.
	j := newJournal(t, vlatte.JournalOptions{MaxEntries: 50})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup

	for w := range 4 {
		wg.Go(func() {
			for i := 0; ctx.Err() == nil; i++ {
				assert.NoError(t, j.Append(entry(fmt.Sprintf("%d-%d", w, i), func(e *vlatte.JournalEntry) { e.Body = []byte("body") })))
			}
		})
	}

	for range 2 {
		wg.Go(func() {
			for ctx.Err() == nil {
				page, err := j.Find(context.Background(), vlatte.JournalQuery{Body: match.Equal("body"), Limit: 10})
				if !assert.NoError(t, err) {
					return
				}

				for _, e := range page.Entries {
					assert.Equal(t, "body", string(e.Body))
				}
			}
		})
	}

	wg.Go(func() {
		for ctx.Err() == nil {
			assert.NoError(t, j.Clear())
			time.Sleep(10 * time.Millisecond)
		}
	})

	wg.Wait()
}

func TestJournal_KeepsRequestsWhoseTextIsntUTF8(t *testing.T) {
	// A client may send a path or a header in another encoding: the request is still journaled, its bad bytes as
	// U+FFFD, the same in the index as in the files, so a query finds it as Get returns it.
	j := newJournal(t, vlatte.JournalOptions{})

	require.NoError(t, j.Append(entry("latin1", func(e *vlatte.JournalEntry) {
		e.Path = "/caf\xe9"
		e.Header = map[string][]string{"X-Name": {"caf\xe9"}}
	})))

	require.Equal(t, []string{"latin1"}, find(t, j, vlatte.JournalQuery{Path: match.Equal("/caf�")}))
	require.Equal(t, []string{"latin1"}, find(t, j, vlatte.JournalQuery{Header: map[string]vlatte.Matcher{"x-name": match.Equal("caf�")}}))

	got, err := j.Get("latin1")
	require.NoError(t, err)
	require.Equal(t, "/caf�", got.Path)
}

func TestJournal_ClipsTextBodiesBetweenCharacters(t *testing.T) {
	// A clipped text body is still text, which the files keep readable: the limit never cuts a character.
	j := newJournal(t, vlatte.JournalOptions{MaxBodySize: 2})

	require.NoError(t, j.Append(entry("a", func(e *vlatte.JournalEntry) { e.Body = []byte("aéb") })))

	got, err := j.Get("a")
	require.NoError(t, err)
	require.Equal(t, "a", string(got.Body))
	require.True(t, got.BodyTruncated)
}

func TestJournal_AHeaderWithoutAMatcherIsAnError(t *testing.T) {
	j := newJournal(t, vlatte.JournalOptions{})

	_, err := j.Find(t.Context(), vlatte.JournalQuery{Header: map[string]vlatte.Matcher{"X": nil}})
	require.ErrorContains(t, err, `header "X" has no matcher`)
}

func TestJournal_ReopensFromTheIndexesOfItsSegments(t *testing.T) {
	// A journal that kept many requests opens fast: it reads its rows from the sidecar of each segment, not from the
	// log. Here the log is overwritten with bytes of its size and time that aren't JSON: only the sidecar can tell
	// the rows.
	dir := t.TempDir()

	j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, j.Append(entry(id)))
	}

	require.NoError(t, j.Close())
	require.FileExists(t, filepath.Join(dir, "journal-000001.idx"))

	log := filepath.Join(dir, "journal-000001.log")
	info, err := os.Stat(log)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(log, bytes.Repeat([]byte("x"), int(info.Size())), 0o600))
	require.NoError(t, os.Chtimes(log, info.ModTime(), info.ModTime()))

	j = newJournal(t, vlatte.JournalOptions{Dir: dir})

	count, err := j.Count(t.Context(), vlatte.JournalQuery{Kind: "http", Path: match.Equal("/b")})
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestJournal_ScansSegmentsItsIndexesDontDescribe(t *testing.T) {
	// A sidecar that is missing, that doesn't decode, or that describes a log of another size or time, such as one
	// written past it, or rewritten, is no source: the journal scans the log, and writes the sidecar anew.
	tests := []struct {
		name  string
		spoil func(t *testing.T, log, idx string)
		want  []string
	}{
		{
			name:  "missing",
			spoil: func(t *testing.T, _, idx string) { require.NoError(t, os.Remove(idx)) },
			want:  []string{"b", "a"},
		},
		{
			name:  "corrupt",
			spoil: func(t *testing.T, _, idx string) { require.NoError(t, os.WriteFile(idx, []byte("{"), 0o600)) },
			want:  []string{"b", "a"},
		},
		{
			name: "written past",
			spoil: func(t *testing.T, log, _ string) {
				f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
				require.NoError(t, err)

				_, err = f.WriteString(`{"seq":3,"id":"late","time":"2026-10-21T12:00:00Z","kind":"http","outcome":"matched"}` + "\n")
				require.NoError(t, err)
				require.NoError(t, f.Close())
			},
			want: []string{"late", "b", "a"},
		},
		{
			name: "rewritten",
			spoil: func(t *testing.T, log, _ string) {
				data, err := os.ReadFile(log)
				require.NoError(t, err)

				// The same size, another first line, and a later time of change.
				data = bytes.Replace(data, []byte(`"id":"a"`), []byte(`"id":"z"`), 1)
				require.NoError(t, os.WriteFile(log, data, 0o600))
				require.NoError(t, os.Chtimes(log, time.Now().Add(time.Minute), time.Now().Add(time.Minute)))
			},
			want: []string{"b", "z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: dir})
			require.NoError(t, err)
			require.NoError(t, j.Append(entry("a")))
			require.NoError(t, j.Append(entry("b")))
			require.NoError(t, j.Close())

			log, idx := filepath.Join(dir, "journal-000001.log"), filepath.Join(dir, "journal-000001.idx")
			tt.spoil(t, log, idx)

			j = newJournal(t, vlatte.JournalOptions{Dir: dir})
			require.Equal(t, tt.want, find(t, j, vlatte.JournalQuery{}))
			describes(t, idx, log)
		})
	}
}

// describes checks that the sidecar idx describes log as it is.
func describes(t *testing.T, idx, log string) {
	t.Helper()

	data, err := os.ReadFile(idx)
	require.NoError(t, err)

	var sidecar struct {
		LogSize    int64 `json:"log_size"`
		LogModTime int64 `json:"log_mod_time"`
	}
	require.NoError(t, json.Unmarshal(data, &sidecar))

	info, err := os.Stat(log)
	require.NoError(t, err)
	require.Equal(t, info.Size(), sidecar.LogSize)
	require.Equal(t, info.ModTime().UnixNano(), sidecar.LogModTime)
}

func TestJournal_RemovesSidecarsWithoutALog(t *testing.T) {
	// A sidecar a failed removal left behind its log describes nothing: the next journal removes it.
	dir := t.TempDir()
	orphan := filepath.Join(dir, "journal-000007.idx")
	require.NoError(t, os.WriteFile(orphan, []byte(`{"rows":[]}`), 0o600))

	newJournal(t, vlatte.JournalOptions{Dir: dir})
	require.NoFileExists(t, orphan)
}

func TestJournal_IndexesEachSegmentItFills(t *testing.T) {
	// A crash loses no sidecar but the active segment's: each full one has its own as soon as the next starts.
	j := newJournal(t, vlatte.JournalOptions{MaxEntries: 4})

	for i := range 3 {
		require.NoError(t, j.Append(entry(strconv.Itoa(i))))
	}

	for _, n := range []string{"1", "2", "3"} {
		describes(t, filepath.Join(j.Dir(), "journal-00000"+n+".idx"), filepath.Join(j.Dir(), "journal-00000"+n+".log"))
	}
}

func TestJournal_TemporaryJournalsWriteNoSidecar(t *testing.T) {
	// Nothing reads the sidecars of a journal whose directory goes with it: it writes none.
	j, err := vlatte.NewJournal(vlatte.JournalOptions{MaxEntries: 4})
	require.NoError(t, err)

	defer j.Close()

	for i := range 3 {
		require.NoError(t, j.Append(entry(strconv.Itoa(i))))
	}

	sidecars, err := filepath.Glob(filepath.Join(j.Dir(), "*.idx"))
	require.NoError(t, err)
	require.Empty(t, sidecars)
}

func TestClipBody(t *testing.T) {
	// Text stays text, clipped between characters; other bytes are cut at the limit.
	tests := []struct {
		name string
		give string
		n    int
		want string
	}{
		{name: "ascii", give: "abcd", n: 2, want: "ab"},
		{name: "between characters", give: "aéb", n: 3, want: "aé"},
		{name: "within a character", give: "aéb", n: 2, want: "a"},
		{name: "within a 4-byte character", give: "a😀", n: 3, want: "a"},
		{name: "binary", give: "\xff\x00\xfe", n: 2, want: "\xff\x00"},
		{name: "binary with a continuation byte", give: "\xff\x80\x80", n: 2, want: "\xff\x80"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, string(vlatte.ClipBody([]byte(tt.give), tt.n)))
		})
	}
}
