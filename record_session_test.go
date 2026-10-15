package vlatte_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// kvCall is a call of a fake protocol: a request for a key, answered with a value and, maybe, a body.
type kvCall struct {
	key       string
	params    map[string]string
	value     string
	status    int
	body      *vlatte.RecordBody
	discarded *atomic.Int64
}

// Discard counts the calls the session didn't record, and drops their body.
func (c kvCall) Discard() {
	if c.discarded != nil {
		c.discarded.Add(1)
	}

	if c.body != nil {
		c.body.Discard()
	}
}

type kvRequest struct {
	Key    string            `json:"key"`
	Params map[string]string `json:"params,omitempty"`
}

type kvResponse struct {
	Value    string `json:"value"`
	Status   int    `json:"status,omitzero"`
	Body     string `json:"body,omitempty"`
	BodyFile string `json:"body_file,omitempty"`
}

type kvMock = vlatte.RecordedMock[kvRequest, kvResponse]

// kvCodec reads kvCalls as mocks. Its hooks block, fail or panic on purpose.
type kvCodec struct {
	block     chan struct{} // Request waits for it to close
	fail      func(kvCall) error
	panicOn   string // a key whose Request panics
	responses atomic.Int64
}

func (c *kvCodec) Name(x kvCall) string { return "GET /" + x.key }

func (c *kvCodec) Request(x kvCall, _ *vlatte.RecordFiles) (kvRequest, error) {
	if c.block != nil {
		<-c.block
	}

	if x.key == c.panicOn && c.panicOn != "" {
		panic("boom")
	}

	if c.fail != nil {
		if err := c.fail(x); err != nil {
			return kvRequest{}, err
		}
	}

	return kvRequest{Key: x.key, Params: x.params}, nil
}

func (c *kvCodec) Response(x kvCall, files *vlatte.RecordFiles) (kvResponse, error) {
	c.responses.Add(1)

	r := kvResponse{Value: x.value, Status: x.status}
	if x.body == nil {
		return r, nil
	}

	if data, ok := x.body.Bytes(); ok {
		if name, inline := files.Place(data, utf8.Valid(data), ".txt"); inline {
			r.Body = string(data)
		} else {
			r.BodyFile = name
		}

		return r, nil
	}

	if err := x.body.Err(); err != nil {
		return r, err
	}

	name, err := files.PlaceSpooled(x.body, ".bin")
	r.BodyFile = name

	return r, err
}

func (c *kvCodec) Mock(m *kvMock) any {
	return map[string]any{"id": m.ID, "name": m.Name, "request": m.Request, "responses": m.Responses}
}

func newSession(t *testing.T, c *kvCodec, o vlatte.RecordOptions[kvRequest, kvResponse]) *vlatte.RecordSession[kvCall, kvRequest, kvResponse] {
	t.Helper()

	if o.Dir == "" {
		o.Dir = t.TempDir()
	}

	if o.Format == "" {
		o.Format = "json"
	}

	s, err := vlatte.NewRecordSession[kvCall](c, o)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = s.Stop(context.Background())
		<-s.Done()
	})

	return s
}

// blockWorker makes the codec block the session's worker until the returned func runs, which a cleanup does too,
// before the session stops: a test that fails while the worker waits doesn't hang.
func blockWorker(t *testing.T, c *kvCodec) func() {
	t.Helper()

	release := sync.OnceFunc(func() { close(c.block) })
	t.Cleanup(release)

	return release
}

// readMock decodes a mock file the kvCodec wrote.
func readMock(t *testing.T, file string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(file)
	require.NoError(t, err)

	doc, err := value.DecodeJSON(data)
	if err != nil {
		require.Fail(t, "not JSON", "%s: %v", data, err)
	}

	m, ok := doc.(map[string]any)
	require.True(t, ok)

	return m
}

func responsesOf(t *testing.T, file string) []any {
	t.Helper()

	r, ok := readMock(t, file)["responses"].([]any)
	require.True(t, ok)

	return r
}

func valuesOf(t *testing.T, file string) []string {
	t.Helper()

	responses := responsesOf(t, file)

	out := make([]string, 0, len(responses))
	for _, r := range responses {
		out = append(out, r.(map[string]any)["value"].(string))
	}

	return out
}

// tempsIn lists the files a recording left half written in dir.
func tempsIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var temps []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			temps = append(temps, e.Name())
		}
	}

	return temps
}

func TestNewRecordSession_ChecksItsOptions(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	tests := []struct {
		name string
		opts vlatte.RecordOptions[kvRequest, kvResponse]
		want string
	}{
		{name: "format", opts: vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "toml"}, want: `recorder: format "toml" is not json, yaml or yml`},
		{name: "no dir", opts: vlatte.RecordOptions[kvRequest, kvResponse]{Format: "json"}, want: "recorder: no directory to record into"},
		{name: "repeats", opts: vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "json", Repeats: 7}, want: "recorder: repeats 7 is not"},
		{name: "negative inline", opts: vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "json", MaxInlineBody: -1}, want: "recorder: MaxInlineBody must be 0 or more, not -1"},
		{name: "dir is a file", opts: vlatte.RecordOptions[kvRequest, kvResponse]{Dir: filepath.Join(file, "sub"), Format: "json"}, want: "recorder: mkdir"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := vlatte.NewRecordSession[kvCall](&kvCodec{}, tc.opts)
			require.ErrorContains(t, err, tc.want)
		})
	}

	_, err := vlatte.NewRecordSession[kvCall, kvRequest, kvResponse](nil, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "json"})
	require.ErrorContains(t, err, "recorder: no codec")
}

func TestRecordSession_Lifecycle(t *testing.T) {
	// One recording at a time writes a directory; a call that comes after it stopped isn't recorded, nor counted.
	var discarded atomic.Int64

	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{})
	require.True(t, s.Status().Active)
	require.True(t, s.Record(kvCall{key: "a", value: "1"}, 10))

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, vlatte.RecordStatus{Active: false, Calls: 1, Mocks: 1}, res.Status)

	require.False(t, s.Record(kvCall{key: "b", value: "2", discarded: &discarded}, 10), "the recording stopped")
	require.Equal(t, int64(1), discarded.Load(), "a call that isn't recorded is discarded")
	require.Equal(t, int64(1), s.Status().Calls)

	again, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Equal(t, res, again, "a later Stop returns what the recording wrote")

	select {
	case <-s.Done():
	default:
		t.Fatal("Done is open after Stop returned")
	}
}

func TestRecordSession_RecordNeverBlocks(t *testing.T) {
	// #33: a recording waited for the previous one to be written. However slow the writing, Record returns at once.
	codec := &kvCodec{block: make(chan struct{})}
	s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{})
	release := blockWorker(t, codec)

	recorded := make(chan bool, 1)
	go func() {
		all := true
		for i := range 10_000 {
			all = s.Record(kvCall{key: strconv.Itoa(i % 50), value: "v"}, 100) && all
		}

		recorded <- all
	}()

	select {
	case all := <-recorded:
		require.True(t, all)
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("Record waited for the writing")
	}

	release()

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 50)
	require.Equal(t, int64(10_000), res.Status.Calls)
}

func TestRecordSession_DropsOverBudget(t *testing.T) {
	// Calls waiting to be written hold memory: past the budget, calls are dropped and counted, never queued without
	// bound. A call is taken whatever its size when nothing waits, so the budget can't refuse every call.
	codec := &kvCodec{block: make(chan struct{})}
	s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{})
	release := blockWorker(t, codec)

	var discarded atomic.Int64

	// The worker takes the first call, and blocks on it: the next ones wait.
	require.True(t, s.Record(kvCall{key: "taken", value: "1"}, 1))
	require.Eventually(t, func() bool { return s.Status().Calls == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // let the worker take it

	require.True(t, s.Record(kvCall{key: "huge", value: "2"}, 1<<40), "nothing waits: taken whatever its size")
	require.False(t, s.Record(kvCall{key: "over", value: "3", discarded: &discarded}, 1))
	require.Equal(t, int64(1), discarded.Load())

	release()

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Status.Dropped)
	require.Len(t, res.Recordings, 2)
}

func TestRecordSession_StopDrainsEveryAcceptedCall(t *testing.T) {
	// Every call Record took is written, and none twice, even when Stop runs while calls come in.
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{})

	var (
		accepted atomic.Int64
		wg       sync.WaitGroup
	)

	for g := range 64 {
		wg.Go(func() {
			for i := range 200 {
				if s.Record(kvCall{key: strconv.Itoa(i % 7), value: fmt.Sprintf("%d-%d", g, i)}, 64) {
					accepted.Add(1)
				}
			}
		})
	}

	time.Sleep(5 * time.Millisecond)

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	wg.Wait()

	written := 0
	seen := make(map[string]bool)

	for _, r := range res.Recordings {
		for _, v := range valuesOf(t, r.File) {
			require.False(t, seen[v], "%s written twice", v)
			seen[v] = true
			written++
		}
	}

	require.Equal(t, int(accepted.Load()), written)
	require.Equal(t, accepted.Load(), res.Status.Calls)
}

func TestRecordSession_GroupsByRequest(t *testing.T) {
	// Calls whose request section writes the same JSON are one request: one mock. Map order never splits them.
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{})

	s.Record(kvCall{key: "a", params: map[string]string{"x": "1", "y": "2"}, value: "1"}, 1)
	s.Record(kvCall{key: "a", params: map[string]string{"y": "2", "x": "1"}, value: "2"}, 1)
	s.Record(kvCall{key: "a", params: map[string]string{"x": "1"}, value: "3"}, 1)
	s.Record(kvCall{key: "b", value: "4"}, 1)

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 3)

	require.Equal(t, []string{"1", "2"}, valuesOf(t, res.Recordings[0].File))
	require.Equal(t, []string{"3"}, valuesOf(t, res.Recordings[1].File))
	require.Equal(t, []string{"4"}, valuesOf(t, res.Recordings[2].File))

	// The ID names the mock and its file: the name, made safe, then the start of the request's hash.
	r := res.Recordings[0]
	require.Regexp(t, `^GET-a-[0-9a-f]{16}$`, r.ID)
	require.Equal(t, "GET /a", r.Name)
	require.Equal(t, r.ID+".mock.json", filepath.Base(r.File))
	require.NotEqual(t, r.ID, res.Recordings[1].ID)
}

func TestRecordSession_Repeats(t *testing.T) {
	tests := []struct {
		name          string
		repeats       vlatte.RecordRepeats
		values        []string
		want          []string
		wantResponses int64
	}{
		{name: "the same response, kept once", repeats: vlatte.RecordSequence, values: []string{"A", "A", "A"}, want: []string{"A"}, wantResponses: 3},
		{name: "two responses", repeats: vlatte.RecordSequence, values: []string{"A", "B"}, want: []string{"A", "B"}, wantResponses: 2},
		{name: "the same responses before another", repeats: vlatte.RecordSequence, values: []string{"A", "A", "B"}, want: []string{"A", "A", "B"}, wantResponses: 3},
		{name: "one coming back", repeats: vlatte.RecordSequence, values: []string{"A", "B", "A"}, want: []string{"A", "B", "A"}, wantResponses: 3},
		// The repeats are ignored before their responses are read: Response runs once.
		{name: "first only", repeats: vlatte.RecordFirst, values: []string{"A", "B", "C"}, want: []string{"A"}, wantResponses: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			codec := &kvCodec{}
			s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{Repeats: tc.repeats})

			for _, v := range tc.values {
				s.Record(kvCall{key: "a", value: v}, 1)
			}

			res, err := s.Stop(t.Context())
			require.NoError(t, err)
			require.Len(t, res.Recordings, 1)
			require.Equal(t, tc.want, valuesOf(t, res.Recordings[0].File))
			require.Equal(t, len(tc.want), res.Recordings[0].Responses)
			require.Equal(t, tc.wantResponses, codec.responses.Load())
		})
	}
}

func TestRecordSession_CoalescesWrites(t *testing.T) {
	// A request that repeats rewrites its mock once per flush interval, not once per call.
	synctest.Test(t, func(t *testing.T) {
		var writes atomic.Int64

		count := func(*kvMock) error {
			writes.Add(1)
			return nil
		}

		s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{
			Transforms: []func(*kvMock) error{count},
		})

		for i := range 1000 {
			s.Record(kvCall{key: "hot", value: strconv.Itoa(i)}, 1)
			if i%100 == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}

		res, err := s.Stop(t.Context())
		require.NoError(t, err)
		require.Len(t, valuesOf(t, res.Recordings[0].File), 1000)
		require.LessOrEqual(t, writes.Load(), int64(2), "about 100 ms of calls: at most one tick, and the last write")
	})
}

func TestRecordSession_Transforms(t *testing.T) {
	t.Run("they change the mock, not its ID", func(t *testing.T) {
		rename := func(m *kvMock) error {
			m.ID = "renamed"
			m.Name = "changed"
			m.Responses[0].Value = "redacted"

			return nil
		}

		s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{
			Transforms: []func(*kvMock) error{rename},
		})
		s.Record(kvCall{key: "a", value: "secret"}, 1)

		res, err := s.Stop(t.Context())
		require.NoError(t, err)

		r := res.Recordings[0]
		require.NotEqual(t, "renamed", r.ID)
		doc := readMock(t, r.File)
		require.Equal(t, r.ID, doc["id"])
		require.Equal(t, "changed", doc["name"])
		require.Equal(t, []string{"redacted"}, valuesOf(t, r.File))
	})

	t.Run("each write starts from what was recorded", func(t *testing.T) {
		// A mock written twice runs its transforms twice: on fresh copies, so they don't add up.
		synctest.Test(t, func(t *testing.T) {
			appendX := func(m *kvMock) error {
				for i := range m.Responses {
					m.Responses[i].Value += "x"
				}

				return nil
			}

			s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{
				Transforms: []func(*kvMock) error{appendX},
			})

			s.Record(kvCall{key: "a", value: "1"}, 1)
			time.Sleep(time.Second)
			s.Record(kvCall{key: "a", value: "2"}, 1)

			res, err := s.Stop(t.Context())
			require.NoError(t, err)
			require.Equal(t, []string{"1x", "2x"}, valuesOf(t, res.Recordings[0].File))
		})
	})

	t.Run("a failing or panicking transform skips its mock only", func(t *testing.T) {
		skip := func(m *kvMock) error {
			switch m.Request.Key {
			case "fails":
				return errors.New("no")
			case "panics":
				panic("boom")
			}

			return nil
		}

		s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{
			Transforms: []func(*kvMock) error{skip},
		})
		s.Record(kvCall{key: "fails", value: "1"}, 1)
		s.Record(kvCall{key: "panics", value: "2"}, 1)
		s.Record(kvCall{key: "ok", value: "3"}, 1)

		res, err := s.Stop(t.Context())
		require.ErrorContains(t, err, "transform 0: no")
		require.ErrorContains(t, err, "panic: boom")
		require.Len(t, res.Recordings, 1)
		require.Equal(t, "GET /ok", res.Recordings[0].Name)
		require.Equal(t, int64(2), res.Status.Failed)
	})
}

func TestRecordSession_FailingCallsAreTheirsOnly(t *testing.T) {
	// A call the codec can't read, or panics on, isn't recorded: the others are, and the worker goes on.
	var discarded atomic.Int64

	codec := &kvCodec{
		panicOn: "panics",
		fail: func(x kvCall) error {
			if x.key == "fails" {
				return errors.New("unreadable")
			}

			return nil
		},
	}

	s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{})
	s.Record(kvCall{key: "fails", value: "1", discarded: &discarded}, 1)
	s.Record(kvCall{key: "panics", value: "2", discarded: &discarded}, 1)
	s.Record(kvCall{key: "ok", value: "3"}, 1)

	res, err := s.Stop(t.Context())
	require.ErrorContains(t, err, "recorder: GET /fails: unreadable")
	require.ErrorContains(t, err, "recorder: recording a call panicked: boom")
	require.Len(t, res.Recordings, 1)
	require.Equal(t, int64(2), res.Status.Failed)
	require.Equal(t, int64(2), discarded.Load())
}

func TestRecordSession_CapsTheErrorsItReturns(t *testing.T) {
	codec := &kvCodec{fail: func(kvCall) error { return errors.New("no") }}
	s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{})

	for i := range 15 {
		s.Record(kvCall{key: strconv.Itoa(i)}, 1)
	}

	_, err := s.Stop(t.Context())
	require.ErrorContains(t, err, "recorder: 5 more failures")
	require.Len(t, strings.Split(err.Error(), "\n"), 11)
}

func TestRecordSession_WritesDocuments(t *testing.T) {
	// The codec returns Go values; the files hold documents, which YAML writes only from the document data model:
	// an int becomes an int64. Both formats read back as the same document.
	for _, format := range []string{"json", "yaml", "yml"} {
		t.Run(format, func(t *testing.T) {
			s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Format: format})
			s.Record(kvCall{key: "a", params: map[string]string{"p": "1"}, value: "v", status: 201}, 1)

			res, err := s.Stop(t.Context())
			require.NoError(t, err)

			file := res.Recordings[0].File
			require.True(t, strings.HasSuffix(file, ".mock."+format), file)

			data, err := os.ReadFile(file)
			require.NoError(t, err)

			var raw any
			require.NoError(t, yaml.Unmarshal(data, &raw), "JSON is YAML too")

			doc, err := value.ViaJSON(raw)
			require.NoError(t, err)

			want := map[string]any{
				"id":        res.Recordings[0].ID,
				"name":      "GET /a",
				"request":   map[string]any{"key": "a", "params": map[string]any{"p": "1"}},
				"responses": []any{map[string]any{"value": "v", "status": int64(201)}},
			}
			require.Equal(t, want, doc)
		})
	}
}

func TestRecordSession_WritesAtomically(t *testing.T) {
	// A mock file is written whole, or not at all: a loader that reads the directory meanwhile never finds half of
	// one, and nothing half written is left behind.
	dir := t.TempDir()
	stale := filepath.Join(dir, ".rec-123.tmp")
	require.NoError(t, os.WriteFile(stale, []byte("half"), 0o600))

	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir})
	require.NoFileExists(t, stale, "a recording that ended unexpectedly left it")

	stop := make(chan struct{})
	var partial atomic.Int64

	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			matches, _ := filepath.Glob(filepath.Join(dir, "*.mock.json"))
			for _, m := range matches {
				if data, err := os.ReadFile(m); err == nil {
					if _, err := value.DecodeJSON(data); err != nil {
						partial.Add(1)
					}
				}
			}
		}
	})

	for i := range 300 {
		s.Record(kvCall{key: strconv.Itoa(i % 5), value: strings.Repeat("v", i)}, 1)
	}

	_, err := s.Stop(t.Context())
	require.NoError(t, err)

	close(stop)
	wg.Wait()

	require.Zero(t, partial.Load())
	require.Empty(t, tempsIn(t, dir))
}

func TestRecordSession_StopHonorsItsContext(t *testing.T) {
	codec := &kvCodec{block: make(chan struct{})}
	s := newSession(t, codec, vlatte.RecordOptions[kvRequest, kvResponse]{})
	release := blockWorker(t, codec)
	s.Record(kvCall{key: "a", value: "1"}, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	_, err := s.Stop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	select {
	case <-s.Done():
		t.Fatal("Done closed while the writing waited")
	default:
	}

	release()

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done never closed")
	}

	// The writing went on after Stop returned: a later Stop returns what it wrote.
	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
}

func TestRecordSession_NamesFiles(t *testing.T) {
	// File names come from the mock's name: safe on every system, short, and never empty or hidden.
	tests := []struct {
		name string
		want string
	}{
		{name: "GET /items?page=2", want: "GET-items-page-2"},
		{name: "../../etc/passwd", want: "etc-passwd"},
		{name: "POST /café/ünïcode", want: "POST-caf-n-code"},
		{name: "   ", want: "recording"},
		{name: ".hidden", want: "hidden"},
		{name: strings.Repeat("a", 500), want: strings.Repeat("a", 80)},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			codec := &namedCodec{name: tc.name}
			s, err := vlatte.NewRecordSession[kvCall](codec, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "json"})
			require.NoError(t, err)

			s.Record(kvCall{key: "a", value: "1"}, 1)

			res, err := s.Stop(t.Context())
			require.NoError(t, err)

			id := res.Recordings[0].ID
			require.Equal(t, tc.want, id[:len(id)-17])
			require.Equal(t, tc.name, res.Recordings[0].Name)
		})
	}
}

// namedCodec names every mock with name.
type namedCodec struct {
	kvCodec

	name string
}

func (c *namedCodec) Name(kvCall) string { return c.name }

func TestRecordSession_PlacesBodies(t *testing.T) {
	// A small text body goes inline. A binary one, one past MaxInlineBody, or any with BodyFiles goes to a file named
	// after its content, so equal bodies share one file.
	tests := []struct {
		name      string
		bodyFiles bool
		body      string
		wantFile  string // the extension of its file; none for an inline body
	}{
		{name: "text", body: "hello"},
		{name: "empty", body: ""},
		{name: "exactly the limit", body: strings.Repeat("a", 64)},
		{name: "past the limit spools", body: strings.Repeat("a", 65), wantFile: ".bin"},
		{name: "binary", body: "\xff", wantFile: ".txt"},
		{name: "every body to a file", bodyFiles: true, body: "hello", wantFile: ".txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, BodyFiles: tc.bodyFiles, MaxInlineBody: 64})

			for _, key := range []string{"a", "b"} {
				body := s.NewBody()
				body.Keep([]byte(tc.body))
				body.Close()
				s.Record(kvCall{key: key, value: "v", body: body}, len(tc.body))
			}

			res, err := s.Stop(t.Context())
			require.NoError(t, err)

			r := responsesOf(t, res.Recordings[0].File)[0].(map[string]any)
			if tc.wantFile == "" {
				require.Equal(t, tc.body, cmpOr(r["body"]))
				require.Empty(t, res.Recordings[0].BodyFiles)

				return
			}

			file, _ := r["body_file"].(string)
			require.Regexp(t, `^GET-a-[0-9a-f]{16}\`+tc.wantFile+`$`, file)
			require.Equal(t, []string{file}, res.Recordings[0].BodyFiles)

			data, err := os.ReadFile(filepath.Join(dir, file))
			require.NoError(t, err)
			require.Equal(t, tc.body, string(data))

			// Two calls, one content: one file.
			entries, _ := filepath.Glob(filepath.Join(dir, "*"+tc.wantFile))
			require.Len(t, entries, 2, "named after each mock: GET-a and GET-b")
		})
	}
}

func cmpOr(v any) string {
	s, _ := v.(string)
	return s
}

func TestRecordSession_SpoolsLargeBodies(t *testing.T) {
	// A body past MaxInlineBody goes to a file while it is captured, then moves into place: the bytes are the
	// body's, and its name comes from them.
	dir := t.TempDir()
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, MaxInlineBody: 10})

	want := strings.Repeat("0123456789", 10_000)

	body := s.NewBody()
	for chunk := range slices.Chunk([]byte(want), 4096) {
		body.Keep(chunk)
	}

	body.Close()

	_, inMemory := body.Bytes()
	require.False(t, inMemory)
	require.Equal(t, int64(len(want)), body.Len())

	s.Record(kvCall{key: "big", value: "v", body: body}, 0)

	res, err := s.Stop(t.Context())
	require.NoError(t, err)

	r := responsesOf(t, res.Recordings[0].File)[0].(map[string]any)
	file := r["body_file"].(string)

	data, err := os.ReadFile(filepath.Join(dir, file))
	require.NoError(t, err)
	require.Equal(t, want, string(data))
	require.Empty(t, tempsIn(t, dir))
}

func TestRecordBody_IsntKeptPastMaxBodySize(t *testing.T) {
	dir := t.TempDir()
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, MaxInlineBody: 10, MaxBodySize: 100})

	var discarded atomic.Int64

	body := s.NewBody()
	for range 20 {
		body.Keep([]byte("0123456789"))
	}

	body.Close()
	require.ErrorContains(t, body.Err(), "larger than MaxBodySize")

	s.Record(kvCall{key: "big", value: "v", body: body, discarded: &discarded}, 0)

	res, err := s.Stop(t.Context())
	require.ErrorContains(t, err, "larger than MaxBodySize")
	require.Empty(t, res.Recordings)
	require.Equal(t, int64(1), discarded.Load())
	require.Empty(t, tempsIn(t, dir))
}

func TestRecordBody_NoLimit(t *testing.T) {
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{MaxInlineBody: 10, MaxBodySize: -1})

	body := s.NewBody()
	body.Keep(make([]byte, 5000))
	body.Close()
	require.NoError(t, body.Err())
	require.Equal(t, int64(5000), body.Len())
}

func TestRecordSession_StopDropsBodiesStillCaptured(t *testing.T) {
	// A call still being captured when the recording stops isn't recorded: its body stops keeping, and its file is
	// removed, so nothing outlives the recording.
	dir := t.TempDir()
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, MaxInlineBody: 10})

	body := s.NewBody()
	body.Keep(make([]byte, 100)) // spooling

	_, err := s.Stop(t.Context())
	require.NoError(t, err)

	body.Keep(make([]byte, 100))
	body.Close()
	require.ErrorContains(t, body.Err(), "the recording stopped")
	require.False(t, s.Record(kvCall{key: "late", body: body}, 0))

	<-s.Done()
	require.Empty(t, tempsIn(t, dir))

	late := s.NewBody()
	late.Keep([]byte("x"))
	require.ErrorContains(t, late.Err(), "the recording stopped")
}

func TestRecordBody_DiscardRemovesItsFile(t *testing.T) {
	dir := t.TempDir()
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, MaxInlineBody: 10})

	body := s.NewBody()
	body.Keep(make([]byte, 100))
	body.Close()
	body.Discard()

	_, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Empty(t, tempsIn(t, dir))
}

func BenchmarkRecordSession_Record(b *testing.B) {
	// The request path of a recording: queueing a call, from many requests at once.
	s, err := vlatte.NewRecordSession[kvCall](&kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: b.TempDir(), Format: "json"})
	require.NoError(b, err)

	b.Cleanup(func() { _, _ = s.Stop(context.Background()) })
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Record(kvCall{key: "k", value: "v"}, 64)
		}
	})
}

func BenchmarkRecordBody_Keep(b *testing.B) {
	// Capturing a 1 MiB body in 32 KiB writes, as a response streams: in memory up to MaxInlineBody, then spooled.
	for _, inline := range []int64{2 << 20, 64 << 10} {
		b.Run(fmt.Sprintf("inline=%dKiB", inline>>10), func(b *testing.B) {
			s, err := vlatte.NewRecordSession[kvCall](&kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{
				Dir: b.TempDir(), Format: "json", MaxInlineBody: inline,
			})
			require.NoError(b, err)

			b.Cleanup(func() { _, _ = s.Stop(context.Background()) })

			chunk := make([]byte, 32<<10)

			b.SetBytes(1 << 20)
			b.ReportAllocs()

			for b.Loop() {
				body := s.NewBody()
				for range 32 {
					body.Keep(chunk)
				}

				body.Close()
				body.Discard()
			}
		})
	}
}

func TestRecordSession_RetriesWhatTheDiskFailed(t *testing.T) {
	// A write the disk fails is tried again at the next flush: a passing failure doesn't lose the mock. It counts as one
	// failure, however many flushes fail.
	synctest.Test(t, func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "rec")
		s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir})

		require.NoError(t, os.RemoveAll(dir))
		s.Record(kvCall{key: "a", value: "1"}, 1)

		time.Sleep(time.Second) // flushes, which fail: the directory is gone
		require.Equal(t, int64(1), s.Status().Failed)
		require.Zero(t, s.Status().Mocks)

		require.NoError(t, os.MkdirAll(dir, 0o755))
		time.Sleep(time.Second) // the next flush writes it

		res, err := s.Stop(t.Context())
		require.ErrorContains(t, err, "no such file or directory")
		require.Len(t, res.Recordings, 1)
		require.Equal(t, int64(1), res.Status.Failed)
		require.FileExists(t, res.Recordings[0].File)
	})
}

func TestRecordSession_AFailingMockCountsOnce(t *testing.T) {
	// A transform that refuses a mock refuses it at each write: the failure counts once, and doesn't push the others
	// out of the errors Stop returns.
	synctest.Test(t, func(t *testing.T) {
		refuse := func(m *kvMock) error {
			if m.Request.Key == "refused" {
				return errors.New("no")
			}

			return nil
		}

		s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Transforms: []func(*kvMock) error{refuse}})

		for i := range 5 {
			s.Record(kvCall{key: "refused", value: strconv.Itoa(i)}, 1)
			time.Sleep(time.Second)
		}

		s.Record(kvCall{key: "other", value: "1"}, 1)

		res, err := s.Stop(t.Context())
		require.Equal(t, "recorder: GET-refused-"+strings.Split(err.Error(), "GET-refused-")[1], err.Error())
		require.Equal(t, int64(1), res.Status.Failed)
		require.Len(t, res.Recordings, 1)
	})
}

func TestRecordSession_KeepsAtMostAThousandResponses(t *testing.T) {
	// A request whose answer keeps changing, such as a clock, would grow its mock without end: a mock keeps 1000
	// responses, and says once that it drops the later ones.
	var logs syncBuffer
	logger := zerolog.New(&logs)

	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Logger: &logger})

	for i := range 1005 {
		s.Record(kvCall{key: "clock", value: strconv.Itoa(i)}, 1)
	}

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1000, res.Recordings[0].Responses)
	require.Equal(t, 1, strings.Count(logs.String(), "later ones aren't recorded"))
}

func TestMustNewRecordSession(t *testing.T) {
	require.Panics(t, func() {
		vlatte.MustNewRecordSession[kvCall](&kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "toml"})
	})

	s := vlatte.MustNewRecordSession[kvCall](&kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: t.TempDir(), Format: "json"})
	_, err := s.Stop(t.Context())
	require.NoError(t, err)
}

func TestRecordSession_BodyFilesReadLikeTheOthers(t *testing.T) {
	// A body that went to a file while captured gets the mode of the files written whole: anyone who reads the mock
	// files reads it too.
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}

	dir := t.TempDir()
	s := newSession(t, &kvCodec{}, vlatte.RecordOptions[kvRequest, kvResponse]{Dir: dir, MaxInlineBody: 10})

	body := s.NewBody()
	body.Keep([]byte(strings.Repeat("x", 100)))
	body.Close()
	s.Record(kvCall{key: "a", value: "v", body: body}, 0)

	res, err := s.Stop(t.Context())
	require.NoError(t, err)

	for _, name := range append([]string{filepath.Base(res.Recordings[0].File)}, res.Recordings[0].BodyFiles...) {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), name)
	}
}

// syncBuffer collects logs, safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
