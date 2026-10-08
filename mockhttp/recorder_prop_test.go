package mockhttp

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// exchange is a request and the response a target gives it.
type exchange struct {
	method    string
	path      string
	query     url.Values
	accept    string
	reqType   string // the request's content type, which names nothing in the recording
	body      *string
	form      *formModel // a multipart form body, in place of body
	status    int
	resHeader map[string]string
	resBody   []byte
	gzip      bool
	level     int // the target's gzip level: real servers compress differently from the replaying server
}

func genExchange() *rapid.Generator[exchange] {
	return rapid.Custom(func(t *rapid.T) exchange {
		ex := exchange{
			method:  rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:    "/" + strings.Join(rapid.SliceOfN(rapid.StringMatching(`[A-Za-z0-9-]{1,6}`), 1, 3).Draw(t, "path"), "/"),
			query:   make(url.Values),
			accept:  genToken().Draw(t, "accept"),
			reqType: rapid.SampledFrom([]string{"", MIMEApplicationJSON, "image/png"}).Draw(t, "request content type"),
			status:  rapid.SampledFrom(_genStatuses).Draw(t, "status"),
			resHeader: map[string]string{
				"Content-Type":  rapid.SampledFrom([]string{"text/plain; charset=utf-8", "application/json", "application/octet-stream"}).Draw(t, "content type"),
				"Cache-Control": genToken().Draw(t, "cache control"),
			},
		}

		for _, k := range rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,5}`), 0, 2, func(s string) string { return s }).Draw(t, "query") {
			ex.query.Set(k, genToken().Draw(t, "query value"))
		}

		if ex.method == "POST" || ex.method == "PUT" || ex.method == "PATCH" {
			if rapid.Bool().Draw(t, "multipart form") {
				// A form records as its fields and files, binary ones to body files: it holds any bytes.
				form := genForm().Draw(t, "form")
				ex.form, ex.reqType = &form, ""
			} else {
				// Other request bodies are UTF-8 text: JSON can't hold the body matcher of one that isn't,
				// which TestRecorder_LogsWhatItCantRecord pins.
				body := genToken().Draw(t, "request body")
				ex.body = &body
			}
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
	if ex.reqType != "" {
		req.header.Set(HeaderContentType, ex.reqType)
	}

	if ex.form != nil {
		// send writes the form under a fresh boundary each time.
		req.multipart, req.files, req.form = true, ex.form.files, make(url.Values)
		for _, f := range ex.form.fields {
			req.form.Add(f.name, f.value)
		}
	}

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

	// The recording holds the body as it reads, in a file named after the response's content type when it goes to
	// one, and records gzip as encoding, which compresses it again and sets Content-Encoding when it replays.
	reply := recordedReply(t, files[0])
	require.Equal(t, ex.gzip, reply.gzip, "encoding")

	for _, h := range reply.header {
		require.False(t, strings.EqualFold(h.name, HeaderContentEncoding), "Content-Encoding is recorded as encoding")
	}

	require.Equal(t, len(ex.resBody) > 0 && (toFile || !utf8.Valid(ex.resBody)), reply.bodyFile != "", "a body file")

	if reply.bodyFile != "" {
		require.Equal(t, bodyFileExt(ex.resHeader[HeaderContentType]), filepath.Ext(reply.bodyFile))

		b, err := os.ReadFile(filepath.Join(dir, reply.bodyFile))
		require.NoError(t, err)
		require.Equal(t, ex.resBody, b, "the body file holds the body as it reads")
	}

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
	require.Equal(t, original.Uncompressed, replayed.Uncompressed, "compressed as the target compressed it")

	for _, h := range []string{"Content-Type", "Cache-Control"} {
		require.Equal(t, original.Header.Get(h), replayed.Header.Get(h), h)
	}

	other := ex.request()
	other.path += "x"

	res, _ := send(t, rt.replay.URL(), other)
	require.Equal(t, StatusNoMatch, res.StatusCode, "another path")
}

// recordedReply reads the reply of a recorded mock file.
func recordedReply(t require.TestingT, path string) *replyDef {
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// The directory, as FromFile sets it: a recorded form compares its binary files with files next to it.
	def := newMockDef()
	def.dir = filepath.Dir(path)
	_, err = _mockSchema.Load(&def, []config.Source{config.Bytes(path, strings.TrimPrefix(filepath.Ext(path), "."), data)})
	require.NoError(t, err)
	require.NotNil(t, def.reply)

	return def.reply
}

// bodyFileExt is the extension of the file the recorder writes a response body to: the extension of the response's
// content type, or .bin.
func bodyFileExt(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	if ext, _ := mime.ExtensionsByType(strings.TrimSpace(mediaType)); len(ext) > 0 {
		return ext[0]
	}

	return ".bin"
}

func TestPropRecorder_RoundTrip(t *testing.T) {
	// M12: a recording replays what it recorded, in every format: the recorded mock matches the original request,
	// query included, and not another path, and answers with the same status, recorded headers and body.
	t.Parallel()

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

	upload := func(form formModel) exchange {
		ex := text(exchange{form: &form})
		ex.method = http.MethodPost

		return ex
	}

	tests := []struct {
		name string
		ex   exchange
	}{
		// Found by TestPropRecorder_FormsRoundTrip: files of fields whose names read the same once made safe for a
		// file name, such as " " and ".", went to one body file, and the last one written won.
		{"files of fields with the same safe name replay", upload(formModel{files: []formFile{
			{field: " ", filename: "a", contentType: "application/json", body: []byte{0xff, '1'}},
			{field: ".", filename: "b", contentType: "application/json", body: []byte{0xff, '2'}},
		}})},
		// F19: the recorded path held the query, which never matched on replay.
		{"a request with a query replays", text(exchange{query: url.Values{"userId": {"7"}}, resBody: []byte("ok")})},
		// Found by TestPropRecorder_RoundTrip: a body that isn't text couldn't be written in a mock file.
		{"a body that isn't text replays", text(exchange{resBody: []byte{0x80, 0xff}})},
		// F22, found by TestPropRecorder_RoundTrip: the recorded Content-Length was replayed
		// over a body compressed again, at another level.
		{"a body gzipped by the target replays", text(exchange{resBody: []byte("A"), gzip: true, level: gzip.BestCompression})},
		// Found by TestPropRecorder_RoundTrip when mock files were Go templates first: {{ in a recording broke its load.
		// Mock files are data now, and recordings are written as they are.
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

func TestRecorder_AFormMatchesByMediaType(t *testing.T) {
	// A recorded upload matches the media type of a Content-Type, as Builder.ContentType does: its case, the spaces
	// around it and the boundary don't matter, as they don't to a server.
	dir := t.TempDir()

	rec := MustNew(Setup().Silent().Record(RecordDir(dir)))
	rec.MustStart()
	t.Cleanup(rec.Close)
	rec.MustMock(AnyMethod().Reply(OK()))

	send(t, rec.URL(), requestSpec{method: http.MethodPost, path: "/x", multipart: true, form: url.Values{"a": {"1"}}})
	rec.StopRecording()

	files, err := filepath.Glob(filepath.Join(dir, "*.mock.*"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	replay := MustNew(Setup().Silent())
	replay.MustStart()
	t.Cleanup(replay.Close)
	replay.MustMock(FromFile(files[0]))

	contentType, body := formModel{fields: []formField{{name: "a", value: "1"}}}.encode(t)
	boundary := boundaryOf(t, contentType)

	tests := []struct {
		contentType string
		status      int
	}{
		{"Multipart/Form-Data; boundary=" + boundary, http.StatusOK},
		{"multipart/form-data ; boundary=" + boundary, http.StatusOK},
		{"multipart/mixed; boundary=" + boundary, StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			res, _ := post(t, replay.URL("/x"), tc.contentType, body)
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}

func TestRecorder_AFormWithFilesItCantReadKeepsItsBody(t *testing.T) {
	// A form whose files can't be read, such as temporary files already removed, isn't recorded as a form: the
	// recording keeps the body the request came with.
	require.Nil(t, formOf(goneFiles(t, formModel{files: []formFile{{field: "f", filename: "a.txt", body: []byte("x")}}})))
}

func TestRecorder_BodyFiles(t *testing.T) {
	// A body file was named after the request's content type, so a JSON response to a GET was saved as .bin, and an
	// image sent back to a POST as text; a gzip body went to the file still compressed, under that name.
	const plain = `{"a": 1}`

	var gz bytes.Buffer

	w := gzip.NewWriter(&gz)
	_, err := w.Write([]byte(plain))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	image := []byte("\x89PNG\r\n\x1a\n")
	text := "a request body"

	tests := []struct {
		name  string
		req   requestSpec
		reply *Reply
		ext   string
		body  []byte
		gzip  bool
	}{
		{
			name:  "a JSON response to a GET without a content type",
			req:   requestSpec{method: http.MethodGet, path: "/a"},
			reply: OK().Header(HeaderContentType, MIMEApplicationCharsetUTF8).Body([]byte(plain)),
			ext:   ".json",
			body:  []byte(plain),
		},
		{
			name:  "an image sent back to a POST of text",
			req:   requestSpec{method: http.MethodPost, path: "/a", body: &text},
			reply: OK().Header(HeaderContentType, "image/png").Body(image),
			ext:   ".png",
			body:  image,
		},
		{
			name:  "a response without a content type",
			req:   requestSpec{method: http.MethodGet, path: "/a"},
			reply: OK().Body([]byte(plain)),
			ext:   ".bin",
			body:  []byte(plain),
		},
		{
			name:  "a gzip JSON response",
			req:   requestSpec{method: http.MethodGet, path: "/a"},
			reply: OK().Header(HeaderContentType, MIMEApplicationJSON).Header(HeaderContentEncoding, "gzip").Body(gz.Bytes()),
			ext:   ".json",
			body:  []byte(plain),
			gzip:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			rec := MustNew(Setup().Silent().Record(RecordDir(dir), RecordResponseBodyToFile(true)))
			rec.MustStart()
			t.Cleanup(rec.Close)
			rec.MustMock(AnyMethod().Reply(tc.reply))

			original, originalBody := send(t, rec.URL(), tc.req)
			rec.StopRecording()

			mocks, err := filepath.Glob(filepath.Join(dir, "*.mock.json"))
			require.NoError(t, err)
			require.Len(t, mocks, 1)

			reply := recordedReply(t, mocks[0])
			require.Equal(t, tc.ext, filepath.Ext(reply.bodyFile), reply.bodyFile)

			b, err := os.ReadFile(filepath.Join(dir, reply.bodyFile))
			require.NoError(t, err)
			require.Equal(t, tc.body, b, "the body as it reads")

			require.Equal(t, tc.gzip, reply.gzip, "encoding")

			for _, h := range reply.header {
				require.False(t, strings.EqualFold(h.name, HeaderContentEncoding), "Content-Encoding is recorded as encoding")
			}

			replay := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.json")))
			replay.MustStart()
			t.Cleanup(replay.Close)

			replayed, replayedBody := send(t, replay.URL(), tc.req)
			require.Equal(t, originalBody, replayedBody)
			require.Equal(t, tc.gzip, replayed.Uncompressed, "compressed again when it replays")
			require.Equal(t, original.Uncompressed, replayed.Uncompressed)
		})
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
	// An exchange the recorder can't write is logged with its cause, and leaves no broken recording behind.
	// The test waits for the recorder's own error: another error log, or a server closed first, proves nothing.
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	notUTF8 := string([]byte{0xff, 0xfe})

	// A gzip stream without the checksum and size that end it: its header reads, its data doesn't.
	var gz bytes.Buffer

	w := gzip.NewWriter(&gz)
	_, err := w.Write([]byte("a body that gzip compresses"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	brokenOff := gz.Bytes()[:gz.Len()-8]

	// A file that isn't UTF-8 goes to a body file named after the request, its form and the field.
	binaryForm := formModel{files: []formFile{{field: "f", filename: "f.bin", body: []byte{0xff, 0xfe}}}}

	tests := []struct {
		name  string
		dir   func(t *testing.T) string
		reply *Reply
		body  *string    // a request body, sent with POST
		form  *formModel // a multipart form, sent with POST
		cause string
	}{
		{
			name: "a form body file it can't write",
			dir: func(t *testing.T) string {
				r, err := stdlibForm(binaryForm.encode(t))
				require.NoError(t, err)

				dir := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(dir, "POST-x--"+formOf(r).hash()+"--f-0.bin"), 0o755))

				return dir
			},
			reply: OK().PlainText("ok"),
			form:  &binaryForm,
			cause: "is a directory",
		},
		{
			name: "a body that isn't gzip",
			dir:  func(t *testing.T) string { return t.TempDir() },
			// Longer than a gzip header, so the reader gets to check it.
			reply: OK().Header(HeaderContentEncoding, "gzip").PlainText("plain text, not gzip"),
			cause: "gzip: invalid header",
		},
		{
			name:  "a gzip stream that breaks off",
			dir:   func(t *testing.T) string { return t.TempDir() },
			reply: OK().Header(HeaderContentEncoding, "gzip").Body(brokenOff),
			cause: "unexpected EOF",
		},
		{
			name:  "a directory it can't make",
			dir:   func(*testing.T) string { return filepath.Join(notADir, "sub") },
			reply: OK().PlainText("ok"),
			cause: "not a directory",
		},
		{
			name: "a body file it can't write",
			dir: func(t *testing.T) string {
				// A body that isn't UTF-8 goes to a file of its own, named after the request, and .bin without a
				// content type.
				dir := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(dir, "GET-x--response-body.bin"), 0o755))

				return dir
			},
			reply: OK().Body([]byte(notUTF8)),
			cause: "is a directory",
		},
		{
			// JSON strings hold UTF-8 only, so the body matcher of this request can't be written as JSON.
			name:  "a request body JSON can't hold",
			dir:   func(t *testing.T) string { return t.TempDir() },
			reply: OK().PlainText("ok"),
			body:  &notUTF8,
			cause: "invalid UTF-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.dir(t)
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			rec := MustNew(Setup().Logger(&logger).Record(RecordDir(dir)))
			rec.MustStart()
			t.Cleanup(rec.Close)
			rec.MustMock(AnyMethod().Reply(tc.reply))

			// The client asks for gzip itself, so it gets the body as sent.
			req := requestSpec{method: http.MethodGet, path: "/x", header: http.Header{"Accept-Encoding": {"gzip"}}}
			if tc.body != nil {
				req.method, req.body = http.MethodPost, tc.body
			}

			if tc.form != nil {
				req.method, req.multipart, req.files = http.MethodPost, true, tc.form.files
			}

			send(t, rec.URL(), req)

			require.Eventually(t, func() bool { return strings.Contains(logs.String(), tc.cause) }, 5*time.Second, 10*time.Millisecond,
				"the recorder logs why it couldn't record")

			files, err := filepath.Glob(filepath.Join(dir, "*.mock.*"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestPropRecorder_FormsRoundTrip(t *testing.T) {
	// M23: a recorded multipart form replays whatever boundary the replayed request has: it records its fields and
	// files, not its bytes. The same upload, sent twice under two boundaries, records to one file; files that aren't
	// UTF-8 text go to body files; several files of a field record as a list; and a file with another byte doesn't
	// replay.
	t.Parallel()

	target, current := newTarget(t)
	rt := newRoundTrip(t, target.URL)

	rapid.Check(t, func(t *rapid.T) {
		ex := genExchange().Draw(t, "exchange")
		form := genForm().Draw(t, "form")
		// Each recorded pattern pins its filename, so a changed byte fails even when two files have the same content.
		for _, filename := range []string{"a.bin", "b.bin"}[:rapid.IntRange(1, 2).Draw(t, "uploads")] {
			form.files = append(form.files, formFile{field: "upload", filename: filename, body: genFileBody().Draw(t, "upload")})
		}

		// 418 is the no-match status too: the replies must tell the two apart.
		ex.method, ex.form, ex.body, ex.reqType, ex.status = http.MethodPost, &form, nil, "", http.StatusOK
		current.Store(&ex)

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		rec, dir := rt.recorder(t, format, false)

		send(t, rec.URL(), ex.request())
		send(t, rec.URL(), ex.request())
		rec.StopRecording()

		mocks, err := filepath.Glob(filepath.Join(dir, "*.mock."+format))
		require.NoError(t, err)
		require.Len(t, mocks, 1, "one upload, one recording; server logs:\n%s", rt.logs)

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)

		contents := make(map[string]bool, len(entries))
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)

			contents[string(b)] = true
		}

		for _, f := range form.files {
			if !utf8.Valid(f.body) {
				require.True(t, contents[string(f.body)], "file %s of %s goes to a body file", f.filename, f.field)
			}
		}

		clearDir(t, rt.replayDir)

		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(rt.replayDir, e.Name()), b, 0o600))
		}

		require.NoError(t, rt.replay.Reload(), "the recording loads")

		replayed, _ := send(t, rt.replay.URL(), ex.request())
		require.Equal(t, ex.status, replayed.StatusCode, "the same form, under another boundary")

		changed := ex
		changedForm := form
		changedForm.files = slices.Clone(form.files)
		last := len(changedForm.files) - 1
		changedForm.files[last].body = append(slices.Clone(changedForm.files[last].body), '~')
		changed.form = &changedForm

		res, _ := send(t, rt.replay.URL(), changed.request())
		require.Equal(t, StatusNoMatch, res.StatusCode, "a file with another byte")
	})
}
