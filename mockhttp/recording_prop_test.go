package mockhttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// callModel is a call a recording takes: a request of a kind the server reads, and the answer the target gives.
type callModel struct {
	method string
	path   string
	query  [][2]string // in the order they are sent, a name repeating
	accept string      // "" sends none
	body   bodyModel

	status  int
	header  http.Header
	payload []byte
	gzip    bool
	trailer http.Header
}

// bodyModel is a request body of a kind the server reads.
type bodyModel struct {
	kind        string // "", json, form, multipart, text or binary
	contentType string
	data        []byte
	form        *formModel
}

// genSegment draws a path segment a proxy sends on: never "." or "..", which could leave the target's path.
func genSegment() *rapid.Generator[string] {
	return genToken().Filter(func(s string) bool { return s != "." && s != ".." })
}

func genCall() *rapid.Generator[callModel] {
	return rapid.Custom(func(t *rapid.T) callModel {
		c := callModel{
			method: rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:   "/" + strings.Join(rapid.SliceOfN(genSegment(), 1, 3).Draw(t, "path"), "/"),
			status: rapid.SampledFrom(_genStatuses).Draw(t, "status"),
			header: make(http.Header),
		}

		names := rapid.SliceOfN(rapid.StringMatching(`[a-c]`), 0, 4).Draw(t, "query names")
		for _, name := range names {
			c.query = append(c.query, [2]string{name, genToken().Draw(t, "query value")})
		}

		if rapid.Bool().Draw(t, "accept") {
			c.accept = genToken().Draw(t, "accept value")
		}

		if c.method != http.MethodGet {
			c.body = genBody().Draw(t, "body")
		}

		c.header.Set(HeaderContentType, rapid.SampledFrom([]string{MIMETextPlain, MIMEApplicationJSON, "image/png", ""}).Draw(t, "content type"))
		if c.header.Get(HeaderContentType) == "" {
			c.header.Del(HeaderContentType)
		}

		for range rapid.IntRange(0, 2).Draw(t, "cookies") {
			c.header.Add("Set-Cookie", genToken().Draw(t, "cookie")+"=1")
		}

		switch rapid.IntRange(0, 2).Draw(t, "payload") {
		case 1:
			c.payload = []byte(rapid.String().Draw(t, "text"))
		case 2:
			c.payload = rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "bytes")
		}

		c.gzip = len(c.payload) > 0 && rapid.Bool().Draw(t, "gzip")

		if rapid.Bool().Draw(t, "trailers") {
			c.trailer = http.Header{"X-Sum": rapid.SliceOfN(genToken(), 1, 2).Draw(t, "trailer")}
		}

		return c
	})
}

func genBody() *rapid.Generator[bodyModel] {
	return rapid.Custom(func(t *rapid.T) bodyModel {
		switch kind := rapid.SampledFrom([]string{"", "json", "form", "multipart", "text", "binary"}).Draw(t, "kind"); kind {
		case "json":
			doc := make(map[string]any)
			for _, k := range rapid.SliceOfN(genToken(), 0, 3).Draw(t, "names") {
				doc[k] = rapid.OneOf(
					rapid.Map(genToken(), func(s string) any { return s }),
					rapid.Map(rapid.Int64(), func(n int64) any { return n }),
					rapid.Map(rapid.SliceOfN(rapid.IntRange(0, 9), 0, 3), func(l []int) any { return l }),
				).Draw(t, "value")
			}

			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("json: %v", err)
			}

			// JSON written another way is the same request.
			if rapid.Bool().Draw(t, "indented") {
				v := jsontext.Value(data)
				if err := v.Indent(); err != nil {
					t.Fatalf("json: %v", err)
				}

				data = v
			}

			return bodyModel{kind: kind, contentType: "application/json; charset=utf-8", data: data}
		case "form":
			var pairs []string
			for _, name := range rapid.SliceOfN(rapid.StringMatching(`[a-c]`), 0, 4).Draw(t, "fields") {
				pairs = append(pairs, name+"="+genToken().Draw(t, "field value"))
			}

			return bodyModel{kind: kind, contentType: MIMEFormURLEncoded, data: []byte(strings.Join(pairs, "&"))}
		case "multipart":
			form := genForm().Draw(t, "form")
			return bodyModel{kind: kind, form: &form}
		case "text":
			return bodyModel{kind: kind, contentType: MIMETextPlain, data: []byte(genToken().Draw(t, "text"))}
		case "binary":
			return bodyModel{kind: kind, contentType: "application/octet-stream", data: []byte{0xff, rapid.Byte().Draw(t, "byte")}}
		default:
			return bodyModel{}
		}
	})
}

// target returns the path and query of the request.
func (c callModel) target() string {
	pairs := make([]string, 0, len(c.query))
	for _, p := range c.query {
		pairs = append(pairs, p[0]+"="+p[1])
	}

	if len(pairs) == 0 {
		return c.path
	}

	return c.path + "?" + strings.Join(pairs, "&")
}

// wire returns the request as it is sent. A multipart form gets a fresh boundary each time.
func (c callModel) wire(t require.TestingT) (http.Header, []byte) {
	header := make(http.Header)
	if c.accept != "" {
		header.Set("Accept", c.accept)
	}

	body := c.body.data
	contentType := c.body.contentType

	if c.body.form != nil {
		contentType, body = c.body.form.encode(t)
	}

	if contentType != "" {
		header.Set(HeaderContentType, contentType)
	}

	return header, body
}

// serverRequest returns the request as a server receives it.
func (c callModel) serverRequest(t require.TestingT) *http.Request {
	header, body := c.wire(t)

	r := httptest.NewRequest(c.method, c.target(), bytes.NewReader(body))
	r.Header = header

	return r
}

// mutated returns a request that differs from c in a part a recording matches, which no request the generators draw
// has: '~' is in no token.
func (c callModel) mutated(t *rapid.T) callModel {
	m := c
	m.query = slices.Clone(c.query)

	parts := []string{"path", "accept"}
	if len(c.query) > 0 {
		parts = append(parts, "query")
	}

	// The server reads the form of a POST, PUT or PATCH only, as http.Request.ParseForm does.
	switch c.body.kind {
	case "json", "text":
		parts = append(parts, c.body.kind)
	case "form":
		if c.method == http.MethodPost || c.method == http.MethodPut || c.method == http.MethodPatch {
			parts = append(parts, "form")
		}
	case "multipart":
		parts = append(parts, "field")
	}

	switch rapid.SampledFrom(parts).Draw(t, "mutated part") {
	case "path":
		m.path += "~"
	case "accept":
		m.accept += "~"
	case "query":
		i := rapid.IntRange(0, len(m.query)-1).Draw(t, "mutated parameter")
		m.query[i][1] += "~"
	case "json":
		m.body.data = []byte(`{"~": ` + string(c.body.data) + `}`)
	case "form":
		m.body.data = append(slices.Clone(c.body.data), "&~=1"...)
	case "text":
		m.body.data = append(slices.Clone(c.body.data), '~')
	case "field":
		form := *c.body.form
		form.fields = append(slices.Clone(form.fields), formField{name: "~", value: "1"})
		m.body.form = &form
	}

	return m
}

// answer writes the target's answer to c.
func (c callModel) answer(w http.ResponseWriter) {
	for k, values := range c.header {
		w.Header()[k] = slices.Clone(values)
	}

	for k := range c.trailer {
		w.Header().Add(HeaderTrailer, k)
	}

	body := c.payload
	if c.gzip {
		var buf bytes.Buffer

		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(body)
		_ = gz.Close()

		body = buf.Bytes()
		w.Header().Set(HeaderContentEncoding, "gzip")
	}

	w.WriteHeader(c.status)
	_, _ = w.Write(body)

	for k, values := range c.trailer {
		w.Header()[k] = slices.Clone(values)
	}
}

// exchange sends a request and reads its whole response.
func exchange(ctx context.Context, t require.TestingT, method, target string, header http.Header, body []byte) (*http.Response, []byte) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	require.NoError(t, err)

	req.Header = header

	res, err := _rawClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// matchingMocks returns the IDs of the mocks of m that match req, a request as the server receives it.
func matchingMocks(t require.TestingT, m *Server, req *http.Request) []string {
	rv, _, _ := parseRequest(req, m)
	if req.MultipartForm != nil {
		defer req.MultipartForm.RemoveAll()
	}

	input := &selectorInput{
		RawRequest: req,
		URL:        rv.URL,
		Query:      req.URL.Query(),
		Form:       req.PostForm,
		Files:      formFiles(req),
		ParsedBody: rv.ParsedBodyData,
	}

	mocks, err := m.storage.List(context.Background())
	require.NoError(t, err)

	var ids []string

	for mock := range mocks {
		ok, err := vlatte.Evaluators[*Mock, *selectorInput](mock.spec.expectations).Eval(context.Background(), vlatte.NewExplanation(), input)
		require.NoError(t, err)

		if ok {
			ids = append(ids, mock.ID())
		}
	}

	return ids
}

func TestPropRecording_RoundTrip(t *testing.T) {
	// A recording answers its request as the target did, in every format, with bodies inline or in files, kept in
	// memory or spooled: the same status, headers, trailers and body.
	var current atomic.Pointer[callModel]

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { current.Load().answer(w) })

	rec := MustNew(Setup().Silent())
	rec.MustStart()
	t.Cleanup(rec.Close)
	rec.MustMock(AnyMethod().Reply(From(target.URL)))

	base := t.TempDir()
	replayDir := filepath.Join(base, "replay")
	require.NoError(t, os.Mkdir(replayDir, 0o700))
	replay := replayServer(t, replayDir)

	n := 0

	rapid.Check(t, func(rt *rapid.T) {
		c := genCall().Draw(rt, "call")
		current.Store(&c)

		n++
		dir := filepath.Join(base, strconv.Itoa(n))

		options := []RecordConfigurer{
			RecordDir(dir),
			RecordFormat(rapid.SampledFrom(MockFileFormats()).Draw(rt, "format")),
			RecordBodyFiles(rapid.Bool().Draw(rt, "body files")),
			RecordMaxInlineBody(rapid.SampledFrom([]int64{0, 16}).Draw(rt, "max inline body")),
			RecordRequestHeaders("Accept"),
			// Every header the target sent, its cookies too.
			RecordExcludeResponseHeaders(),
		}

		require.NoError(rt, rec.StartRecording(options...))

		header, body := c.wire(rt)
		original, originalBody := exchange(t.Context(), rt, c.method, rec.URL()+c.target(), header, body)

		res, err := rec.StopRecording(t.Context())
		require.NoError(rt, err)
		require.Len(rt, res.Recordings, 1)

		clearDir(rt, replayDir)

		entries, err := os.ReadDir(dir)
		require.NoError(rt, err)

		for _, e := range entries {
			require.False(rt, strings.HasSuffix(e.Name(), ".tmp"), "a temporary file is left: %s", e.Name())

			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(rt, err)
			require.NoError(rt, os.WriteFile(filepath.Join(replayDir, e.Name()), b, 0o600))
		}

		require.NoError(rt, replay.Reload(), "the recording loads")

		header, body = c.wire(rt)
		replayed, replayedBody := exchange(t.Context(), rt, c.method, replay.URL()+c.target(), header, body)
		requireSameAnswer(rt, original, replayed, originalBody, replayedBody)
	})
}

func TestPropRecording_OneRecordingPerRequest(t *testing.T) {
	// Each request a recording took matches one recorded mock, and a request that differs in a part a recording
	// matches matches none: a recording never answers a request it didn't record.
	rec := MustNew(Setup().Silent())
	rec.MustStart()
	t.Cleanup(rec.Close)
	rec.MustMock(AnyMethod().Reply(OK().PlainText("ok")))

	base := t.TempDir()
	n := 0

	rapid.Check(t, func(rt *rapid.T) {
		calls := rapid.SliceOfN(genCall(), 1, 5).Draw(rt, "calls")

		n++
		dir := filepath.Join(base, strconv.Itoa(n))
		require.NoError(rt, rec.StartRecording(RecordDir(dir), RecordRequestHeaders("Accept"), RecordIncludeMocked(true)))

		for _, c := range calls {
			header, body := c.wire(rt)
			res, _ := exchange(t.Context(), rt, c.method, rec.URL()+c.target(), header, body)
			require.Equal(rt, http.StatusOK, res.StatusCode)
		}

		_, err := rec.StopRecording(t.Context())
		require.NoError(rt, err)

		replay := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.json")))
		require.NoError(rt, replay.Reload())

		for i, c := range calls {
			require.Len(rt, matchingMocks(rt, replay, c.serverRequest(rt)), 1, "call %d: %s %s", i, c.method, c.target())

			other := c.mutated(rt)
			require.Empty(rt, matchingMocks(rt, replay, other.serverRequest(rt)), "call %d changed: %s %s", i, other.method, other.target())
		}
	})
}
