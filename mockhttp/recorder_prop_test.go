package mockhttp

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// exchange is a request and the response a target gives it.
type exchange struct {
	method    string
	path      string
	query     url.Values
	accept    string
	body      *string
	status    int
	resHeader map[string]string
	resBody   []byte
	gzip      bool
	level     int // the target's gzip level: real servers compress differently from the replaying server
}

func genExchange() *rapid.Generator[exchange] {
	return rapid.Custom(func(t *rapid.T) exchange {
		ex := exchange{
			method: rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:   "/" + strings.Join(rapid.SliceOfN(rapid.StringMatching(`[A-Za-z0-9-]{1,6}`), 1, 3).Draw(t, "path"), "/"),
			query:  make(url.Values),
			accept: genToken().Draw(t, "accept"),
			status: rapid.SampledFrom(_genStatuses).Draw(t, "status"),
			resHeader: map[string]string{
				"Content-Type":  rapid.SampledFrom([]string{"text/plain; charset=utf-8", "application/json", "application/octet-stream"}).Draw(t, "content type"),
				"Cache-Control": genToken().Draw(t, "cache control"),
			},
		}

		for _, k := range rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,5}`), 0, 2, func(s string) string { return s }).Draw(t, "query") {
			ex.query.Set(k, genToken().Draw(t, "query value"))
		}

		if ex.method == "POST" || ex.method == "PUT" || ex.method == "PATCH" {
			body := genToken().Draw(t, "request body")
			ex.body = &body
		}

		switch rapid.IntRange(0, 2).Draw(t, "response body") {
		case 1:
			ex.resBody = []byte(rapid.String().Draw(t, "text"))
		case 2:
			ex.resBody = rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "bytes")
		}

		ex.gzip = len(ex.resBody) > 0 && rapid.Bool().Draw(t, "gzip")
		ex.level = rapid.SampledFrom([]int{gzip.HuffmanOnly, gzip.BestSpeed, gzip.DefaultCompression, gzip.BestCompression}).Draw(t, "gzip level")

		return ex
	})
}

func (ex exchange) request() requestSpec {
	req := requestSpec{method: ex.method, path: ex.path, query: ex.query, header: http.Header{"Accept": {ex.accept}}, body: ex.body}
	return req
}

// newTarget starts a server that answers with the response of the current exchange.
func newTarget(t *testing.T) (*httptest.Server, *atomic.Pointer[exchange]) {
	var current atomic.Pointer[exchange]

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ex := current.Load()
		for k, v := range ex.resHeader {
			w.Header().Set(k, v)
		}

		body := ex.resBody
		if ex.gzip {
			var buf bytes.Buffer

			gz, _ := gzip.NewWriterLevel(&buf, ex.level)
			gz.Write(body)
			gz.Close()

			body = buf.Bytes()
			w.Header().Set(HeaderContentEncoding, "gzip")
		}

		w.WriteHeader(ex.status)
		w.Write(body)
	}))
	t.Cleanup(target.Close)

	return target, &current
}

// roundTrip records exchanges and replays the recordings. Its servers live across exchanges, so thousands of them
// don't run out of local ports: new servers and connections for each would leave a socket in TIME_WAIT each.
type roundTrip struct {
	target    string
	base      string
	recorders map[string]*Server // by format and body mode
	replay    *Server
	replayDir string
	logs      *logBuffer
}

func newRoundTrip(t *testing.T, target string) *roundTrip {
	// The recorder reports what it can't record in the logs, so they explain a missing recording.
	logs := &logBuffer{}

	base := t.TempDir()
	replayDir := filepath.Join(base, "replay")
	require.NoError(t, os.Mkdir(replayDir, 0o700))

	replay := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(replayDir, "*.mock.*")))
	replay.MustStart()

	rt := &roundTrip{target: target, base: base, recorders: make(map[string]*Server), replay: replay, replayDir: replayDir, logs: logs}

	t.Cleanup(func() {
		replay.Close()
		for _, rec := range rt.recorders {
			rec.Close()
		}
	})

	return rt
}

// recorder returns the recording server of a format and body mode, with its recordings cleared.
func (rt *roundTrip) recorder(t require.TestingT, format string, toFile bool) (*Server, string) {
	dir := filepath.Join(rt.base, format+"-"+strconv.FormatBool(toFile))

	rec, ok := rt.recorders[dir]
	if !ok {
		require.NoError(t, os.Mkdir(dir, 0o700))

		logger := zerolog.New(rt.logs).Level(zerolog.WarnLevel)
		rec = MustNew(Setup().Logger(&logger).Record(RecordDir(dir), RecordExtension(format), RecordResponseBodyToFile(toFile)))
		rec.MustStart()
		rec.MustMock(AnyMethod().Reply(From(rt.target)))
		rt.recorders[dir] = rec
	}

	clearDir(t, dir)
	rec.rec.start()

	return rec, dir
}

func clearDir(t require.TestingT, dir string) {
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
}

// requireRoundTrip records ex, then replays the recording: it must match the original request and not another
// path, and answer as the target did.
func (rt *roundTrip) requireRoundTrip(t require.TestingT, ex exchange, format string, toFile bool) {
	rec, dir := rt.recorder(t, format, toFile)

	original, originalBody := send(t, rec.URL(), ex.request())

	// The response reaches the client once the handler returns, after it handed the exchange to the recorder;
	// stopping waits for the recorder to write it.
	rec.StopRecording()

	files, err := filepath.Glob(filepath.Join(dir, "*.mock."+format))
	require.NoError(t, err)
	require.Len(t, files, 1, "one recording; server logs:\n%s", rt.logs)

	// The replay server loads the recording and its body files, which replace the last ones.
	clearDir(t, rt.replayDir)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(rt.replayDir, e.Name()), b, 0o600))
	}

	require.NoError(t, rt.replay.Reload(), "the recording loads")

	replayed, replayedBody := send(t, rt.replay.URL(), ex.request())
	require.Equal(t, original.StatusCode, replayed.StatusCode, "status")
	require.Equal(t, originalBody, replayedBody, "body")

	for _, h := range []string{"Content-Type", "Cache-Control"} {
		require.Equal(t, original.Header.Get(h), replayed.Header.Get(h), h)
	}

	other := ex.request()
	other.path += "x"

	res, _ := send(t, rt.replay.URL(), other)
	require.Equal(t, StatusNoMatch, res.StatusCode, "another path")
}

func TestPropRecorder_RoundTrip(t *testing.T) {
	// M12: a recording replays what it recorded, in every format: the recorded mock matches the original request,
	// query included, and not another path, and answers with the same status, recorded headers and body.
	target, current := newTarget(t)
	rt := newRoundTrip(t, target.URL)

	rapid.Check(t, func(t *rapid.T) {
		ex := genExchange().Draw(t, "exchange")
		current.Store(&ex)

		rt.requireRoundTrip(t, ex, rapid.SampledFrom(MockFileFormats()).Draw(t, "format"), rapid.Bool().Draw(t, "body to a file"))
	})
}

func TestRecorder_Regressions(t *testing.T) {
	target, current := newTarget(t)

	text := func(ex exchange) exchange {
		ex.method, ex.path, ex.status, ex.accept = http.MethodGet, "/a", http.StatusOK, "x"
		ex.resHeader = map[string]string{"Content-Type": "text/plain", "Cache-Control": "no-cache"}
		if ex.query == nil {
			ex.query = make(url.Values)
		}

		return ex
	}

	tests := []struct {
		name string
		ex   exchange
	}{
		// F19: the recorded path held the query, which never matched on replay.
		{"a request with a query replays", text(exchange{query: url.Values{"userId": {"7"}}, resBody: []byte("ok")})},
		// Found by TestPropRecorder_RoundTrip: a body that isn't text couldn't be written in a mock file.
		{"a body that isn't text replays", text(exchange{resBody: []byte{0x80, 0xff}})},
		// F22, found by TestPropRecorder_RoundTrip: the recorded Content-Length was replayed
		// over a body compressed again, at another level.
		{"a body gzipped by the target replays", text(exchange{resBody: []byte("A"), gzip: true, level: gzip.BestCompression})},
		// Found by TestPropRecorder_RoundTrip: a mock file is a Go template first, so {{ in a recording broke its load.
		{"a body with template delimiters replays as is", text(exchange{
			query:   url.Values{"q": {"{{"}},
			resBody: []byte("{{ .Request.Method }} {{`{{`}} {{{ }}"),
		})},
	}

	rt := newRoundTrip(t, target.URL)

	for _, tc := range tests {
		for _, format := range MockFileFormats() {
			t.Run(tc.name+" as "+format, func(t *testing.T) {
				ex := tc.ex
				current.Store(&ex)
				rt.requireRoundTrip(t, ex, format, false)
			})
		}
	}
}

func TestRecorder_Formats(t *testing.T) {
	// Recordings are mock files, so they use the formats mock files can: any other fails when the server is created.
	_, err := New(Setup().Record(RecordExtension("toml")))
	require.EqualError(t, err, `server: recorder: recorded mock file format "toml" is not supported: use json, yaml, yml`)

	m, err := New(Setup().Record(RecordExtension(".YAML")))
	require.NoError(t, err)
	require.Equal(t, "yaml", m.config.Record.SaveFileType)
}

func TestRecorder_LogsWhatItCantRecord(t *testing.T) {
	// A response the recorder can't write is logged, and leaves no broken recording behind.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentEncoding, "gzip")
		w.Write([]byte("not gzip"))
	}))
	t.Cleanup(target.Close)

	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	for name, dir := range map[string]string{
		"a body that isn't gzip":    t.TempDir(),
		"a directory it can't make": filepath.Join(notADir, "sub"),
	} {
		t.Run(name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			rec := MustNew(Setup().Logger(&logger).Record(RecordDir(dir)))
			rec.MustStart()
			rec.MustMock(AnyMethod().Reply(From(target.URL)))

			// The client asks for gzip itself, so it gets the body as sent.
			send(t, rec.URL(), requestSpec{method: http.MethodGet, path: "/x", header: http.Header{"Accept-Encoding": {"gzip"}}})
			rec.Close()

			require.Contains(t, logs.String(), `"level":"error"`)

			files, err := filepath.Glob(filepath.Join(dir, "*.mock.*"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}
