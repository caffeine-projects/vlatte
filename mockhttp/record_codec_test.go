package mockhttp

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
)

// sentRequest is a request a test sends.
type sentRequest struct {
	method string
	target string // the path and query
	header http.Header
	body   []byte
}

func (r sentRequest) send(t *testing.T, base string) (*http.Response, []byte) {
	t.Helper()

	return do(t, cmp.Or(r.method, http.MethodGet), base+r.target, r.header, r.body)
}

// textParser reads application/x-custom bodies, as a parser of a server's config does.
type textParser struct{}

func (textParser) CanParse(contentType string, _ *http.Request) bool {
	return mediaType(contentType) == "application/x-custom"
}

func (textParser) Parse(body []byte, _ *http.Request) (any, error) { return string(body), nil }

// bodyFileName is the name a recording gives a body file of the mock named after slug.
func bodyFileName(slug string, content []byte, ext string) string {
	sum := sha256.Sum256(content)
	return slug + "-" + hex.EncodeToString(sum[:])[:16] + ext
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := gzip.NewWriter(&buf)
	_, err := w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	return buf.Bytes()
}

func TestCanonicalURL(t *testing.T) {
	// Requests a recording can't tell apart write one URL, so they are one mock; requests it can, write two.
	tests := []struct {
		target string
		want   string
	}{
		{"/items", "/items"},
		{"/", "/"},
		{"/items?", "/items"},
		{"/items?&", "/items"},
		{"/items?b=2&a=1", "/items?a=1&b=2"},
		{"/items?a=2&a=1", "/items?a=2&a=1"},
		{"/items?a", "/items?a="},
		{"/items?a=x+y", "/items?a=x+y"},
		{"/items?a=x%20y", "/items?a=x+y"},
		{"/items/", "/items/"},
		{"/a%2Fb", "/a%2Fb"},
		{"/a%2fb", "/a%2Fb"},
		{"/%41", "/A"},
		{"/a+b", "/a+b"},
		{"/a%20b", "/a%20b"},
		{"/caf%C3%A9", "/caf%C3%A9"},
		{"/a;b,c", "/a;b,c"},
		{"/a%3Fb", "/a%3Fb"},
		{"//double", "//double"},
		{"/items?a=1;b=2", "/items?a=1;b=2"},
		{"/items?a=%zz", "/items?a=%zz"},
		{"/files/*", "/files/%2A"},
		{"/files/%2a", "/files/%2A"},
		{"//a:b/c", "//a:b/c"},
	}

	for _, tc := range tests {
		u, err := url.ParseRequestURI(tc.target)
		require.NoError(t, err, tc.target)

		got, err := canonicalURL(u)
		require.NoError(t, err, tc.target)
		require.Equal(t, tc.want, got, tc.target)
	}

	t.Run("a query that doesn't parse, and isn't UTF-8, isn't recorded", func(t *testing.T) {
		_, err := canonicalURL(&url.URL{Path: "/a", RawQuery: "a=1;\xff"})
		require.ErrorIs(t, err, errQueryNotUTF8)
	})

	t.Run("an empty path is the root, as in a request", func(t *testing.T) {
		// A forward proxy reads http://host?a=1 with no path.
		got, err := canonicalURL(&url.URL{RawQuery: "a=1"})
		require.NoError(t, err)
		require.Equal(t, "/?a=1", got)
	})
}

func FuzzCanonicalURL(f *testing.F) {
	// A recording matches the request it came from: the URL it writes is one match.RequestURI matches the request's
	// URL with, the way the server reads it. Written again, it doesn't change.
	for _, seed := range []string{
		"/items?b=2&a=1", "/a%2Fb", "/%41", "//double", "/a;b,c", "/x?a=1;b=2", "/caf%C3%A9?q=%E2%9C%93", "/a+b?c=d+e", "/?",
		"/files/*", "/files/%2A", "//a:b/c", "//a%20b/c", "//[x",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, target string) {
		// The server reads a target that starts with "/", and cuts its fragment off.
		u, err := url.ParseRequestURI(target)
		if err != nil || !strings.HasPrefix(target, "/") || strings.Contains(target, "#") {
			return
		}

		canonical, err := canonicalURL(u)
		if err != nil {
			require.ErrorIs(t, err, errQueryNotUTF8)
			require.False(t, utf8.ValidString(u.RawQuery))

			return
		}

		served := &url.URL{Scheme: "http", Host: "vlatte", Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}

		ok, err := match.RequestURI(canonical).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), served.String()))
		require.NoError(t, err)
		require.True(t, ok, "%q doesn't match %q", canonical, target)

		require.NoError(t, setURL(&requestDef{}, config.Value{Key: "request.url", Any: canonical}), "a mock file takes %q", canonical)

		again, err := url.ParseRequestURI(canonical)
		require.NoError(t, err)

		rewritten, err := canonicalURL(again)
		require.NoError(t, err)
		require.Equal(t, canonical, rewritten)
	})
}

func TestBodyExt(t *testing.T) {
	tests := map[string]string{
		"application/json":                ".json",
		"application/json; charset=utf-8": ".json",
		"Application/JSON":                ".json",
		"application/problem+json":        ".json",
		"application/atom+xml":            ".xml",
		"text/xml":                        ".xml",
		"text/html; charset=utf-8":        ".html",
		"text/plain":                      ".txt",
		"text/csv":                        ".csv",
		"image/png":                       ".png",
		"image/jpeg":                      ".jpg",
		"image/svg+xml":                   ".svg",
		"application/zip":                 ".zip",
		"application/octet-stream":        ".bin",
		"":                                ".bin",
		"not a type":                      ".bin",
	}

	for contentType, want := range tests {
		require.Equal(t, want, bodyExt(contentType), contentType)
	}
}

func TestRecording_RequestSection(t *testing.T) {
	// A recording matches the request it recorded, and no other. What it can't read of a request, it doesn't match.
	contentType := func(media string) MatcherSpec {
		return MatcherSpec{"split", ";", []any{"item", 0, []any{"trim", []any{"eqi", media}}}}
	}

	notPresent := MatcherSpec{"not", "present"}
	jsonType := http.Header{HeaderContentType: {"application/json; charset=utf-8"}}
	textType := http.Header{HeaderContentType: {MIMETextPlain}}
	formType := http.Header{HeaderContentType: {MIMEFormURLEncoded}}

	upload := formModel{
		fields: []formField{{name: "x", value: "1"}},
		files: []formFile{
			{field: "f", filename: "a.txt", contentType: "text/plain; charset=utf-8", body: []byte("hello")},
			{field: "g", filename: "b.png", contentType: "image/png", body: []byte{0xff, 0x00}},
		},
	}

	multipartRequest := func(form formModel) sentRequest {
		contentType, body := form.encode(t)
		return sentRequest{method: http.MethodPost, target: "/upload", header: http.Header{HeaderContentType: {contentType}}, body: body}
	}

	changedFile := upload
	changedFile.files = []formFile{upload.files[0], {field: "g", filename: "b.png", contentType: "image/png", body: []byte{0xff, 0x01}}}

	changedField := upload
	changedField.fields = []formField{{name: "x", value: "2"}}

	tests := []struct {
		name    string
		setup   func() *ConfigBuilder
		options []RecordConfigurer
		request sentRequest
		want    RecordedRequest
		match   []sentRequest
		miss    []sentRequest
	}{
		{
			name:    "a GET",
			request: sentRequest{target: "/items?page=2"},
			want:    RecordedRequest{Method: http.MethodGet, URL: "/items?page=2"},
			match:   []sentRequest{{target: "/items?page=2&"}},
			miss: []sentRequest{
				{target: "/items"},
				{target: "/items?page=3"},
				{target: "/items?page=2&x=1"},
				{target: "/items/?page=2"},
				{method: http.MethodDelete, target: "/items?page=2"},
			},
		},
		{
			name:    "an escaped path",
			request: sentRequest{target: "/a%2Fb/c%41"},
			want:    RecordedRequest{Method: http.MethodGet, URL: "/a%2Fb/cA"},
			match:   []sentRequest{{target: "/a%2fb/cA"}},
			miss:    []sentRequest{{target: "/a/b/cA"}},
		},
		{
			name:    "a JSON body",
			request: sentRequest{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"b": 1, "a": [1, 2]}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/json")},
				Body:   MatcherSpec{"eqj", `{"a":[1,2],"b":1}`},
			},
			match: []sentRequest{
				{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {"Application/JSON"}}, body: []byte(`{"a":[1,2],"b":1.0}`)},
			},
			miss: []sentRequest{
				{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"a":[2,1],"b":1}`)},
				{method: http.MethodPost, target: "/j", header: textType, body: []byte(`{"b": 1, "a": [1, 2]}`)},
			},
		},
		{
			name:    "a JSON media type of its own",
			request: sentRequest{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {"application/vnd.api+json"}}, body: []byte(`{"a": 1}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/vnd.api+json")},
				Body:   MatcherSpec{"eqj", `{"a":1}`},
			},
		},
		{
			name:    "an integer past the precision of a float keeps every digit",
			request: sentRequest{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"n": 9007199254740993}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/json")},
				Body:   MatcherSpec{"eqj", `{"n":9007199254740993}`},
			},
			miss: []sentRequest{{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"n": 9007199254740992}`)}},
		},
		{
			name:    "a number past int64 still matches itself",
			request: sentRequest{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"n": 123456789012345678901234567890}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/json")},
				Body:   MatcherSpec{"eqj", `{"n":1.2345678901234568e+29}`},
			},
		},
		{
			name:    "JSON with a name twice matches as text",
			request: sentRequest{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"a":1,"a":2}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/json")},
				Body:   MatcherSpec{"equal", `{"a":1,"a":2}`},
			},
			miss: []sentRequest{{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{"a":2}`)}},
		},
		{
			name:    "a urlencoded form",
			request: sentRequest{method: http.MethodPost, target: "/f", header: formType, body: []byte("b=2&a=1&a=3")},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/f",
				Header: map[string]MatcherSpec{HeaderContentType: contentType(MIMEFormURLEncoded)},
				Body:   MatcherSpec{"equal", map[string]any{"a": []any{"1", "3"}, "b": []any{"2"}}},
			},
			match: []sentRequest{{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=1&b=2&a=3")}},
			miss: []sentRequest{
				{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=3&a=1&b=2")},
				{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=1&a=3")},
				{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=1&a=3&b=2&c=4")},
			},
		},
		{
			// Found by TestPropRecording_OneRecordingPerRequest: the server reads the form of a POST, PUT or PATCH only.
			name:    "a form the server doesn't read",
			request: sentRequest{method: http.MethodDelete, target: "/f", header: formType, body: []byte("a=1")},
			want:    RecordedRequest{Method: http.MethodDelete, URL: "/f"},
			match:   []sentRequest{{method: http.MethodDelete, target: "/f", header: formType, body: []byte("a=2")}},
		},
		{
			// The server doesn't read it either: it serves the request all the same.
			name:    "a form that doesn't parse",
			request: sentRequest{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=%zz")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/f"},
			match:   []sentRequest{{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=1")}},
		},
		{
			name:    "a form name that isn't UTF-8 text",
			request: sentRequest{method: http.MethodPost, target: "/f", header: formType, body: []byte("%ff=1")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/f"},
			match:   []sentRequest{{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=1")}},
		},
		{
			name:    "a form value that isn't UTF-8 text",
			request: sentRequest{method: http.MethodPost, target: "/f", header: formType, body: []byte("a=%ff")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/f"},
		},
		{
			name:    "a multipart field that isn't UTF-8 text",
			request: multipartRequest(formModel{fields: []formField{{name: "x", value: "\xff"}}}),
			want:    RecordedRequest{Method: http.MethodPost, URL: "/upload"},
			match:   []sentRequest{multipartRequest(formModel{fields: []formField{{name: "x", value: "1"}}})},
		},
		{
			name: "a form file whose name isn't UTF-8 text isn't matched, and the others are",
			request: multipartRequest(formModel{files: []formFile{
				{field: "f", filename: "\xff.txt", contentType: MIMETextPlain, body: []byte("a")},
				{field: "g", filename: "b.txt", contentType: MIMETextPlain, body: []byte("b")},
			}}),
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/upload",
				Header:   map[string]MatcherSpec{HeaderContentType: contentType(MIMEMultipartForm)},
				Body:     MatcherSpec{"equal", make(map[string]any)},
				FormFile: map[string][]RecordedFile{"g": {{Filename: "b.txt", ContentType: MIMETextPlain, Body: MatcherSpec{"equal", "b"}}}},
			},
		},
		{
			name:    "a multipart form",
			request: multipartRequest(upload),
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/upload",
				Header: map[string]MatcherSpec{HeaderContentType: contentType(MIMEMultipartForm)},
				Body:   MatcherSpec{"equal", map[string]any{"x": []any{"1"}}},
				FormFile: map[string][]RecordedFile{
					"f": {{Filename: "a.txt", ContentType: "text/plain", Body: MatcherSpec{"equal", "hello"}}},
					"g": {{Filename: "b.png", ContentType: "image/png", BodyFile: bodyFileName("POST-upload", []byte{0xff, 0x00}, ".png")}},
				},
			},
			match: []sentRequest{multipartRequest(upload)},
			miss:  []sentRequest{multipartRequest(changedFile), multipartRequest(changedField)},
		},
		{
			name:    "a text body",
			request: sentRequest{method: http.MethodPost, target: "/t", header: textType, body: []byte("hello")},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/t",
				Header: map[string]MatcherSpec{HeaderContentType: contentType(MIMETextPlain)},
				Body:   MatcherSpec{"equal", "hello"},
			},
			miss: []sentRequest{{method: http.MethodPost, target: "/t", header: textType, body: []byte("hello!")}},
		},
		{
			name:    "a body without a Content-Type",
			request: sentRequest{method: http.MethodPost, target: "/n", body: []byte("hello")},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/n",
				Header: map[string]MatcherSpec{HeaderContentType: notPresent},
				Body:   MatcherSpec{"equal", "hello"},
			},
			miss: []sentRequest{{method: http.MethodPost, target: "/n", header: textType, body: []byte("hello")}},
		},
		{
			name:    "a POST without a body",
			request: sentRequest{method: http.MethodPost, target: "/e"},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/e", Body: MatcherSpec{"empty"}},
			miss:    []sentRequest{{method: http.MethodPost, target: "/e", header: textType, body: []byte("x")}},
		},
		{
			name:    "a GET with a body",
			request: sentRequest{target: "/g", header: textType, body: []byte("x")},
			want:    RecordedRequest{Method: http.MethodGet, URL: "/g"},
			match:   []sentRequest{{target: "/g", header: textType, body: []byte("y")}},
		},
		{
			name:    "a body that isn't text",
			request: sentRequest{method: http.MethodPost, target: "/b", header: http.Header{HeaderContentType: {"application/octet-stream"}}, body: []byte{0xff, 0xfe}},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/b"},
			match:   []sentRequest{{method: http.MethodPost, target: "/b", body: []byte{0x00}}},
		},
		{
			name:    "a body larger than MaxInlineBody",
			options: []RecordConfigurer{RecordMaxInlineBody(4)},
			request: sentRequest{method: http.MethodPost, target: "/l", header: textType, body: []byte("12345")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/l"},
		},
		{
			name:    "a body the server read part of",
			setup:   func() *ConfigBuilder { return Setup().MaxBodyParsingLimit(5) },
			request: sentRequest{method: http.MethodPost, target: "/c", header: textType, body: []byte("123456")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/c"},
			match:   []sentRequest{{method: http.MethodPost, target: "/c", header: textType, body: []byte("1234567")}},
		},
		{
			name:    "a body exactly at the limit, which may have had more",
			setup:   func() *ConfigBuilder { return Setup().MaxBodyParsingLimit(5) },
			request: sentRequest{method: http.MethodPost, target: "/c", header: textType, body: []byte("12345")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/c"},
		},
		{
			name:    "a server that parses no body",
			setup:   func() *ConfigBuilder { return Setup().NoBodyParsing() },
			request: sentRequest{method: http.MethodPost, target: "/np"},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/np"},
			match:   []sentRequest{{method: http.MethodPost, target: "/np", body: []byte("x")}},
		},
		{
			name:    "a body a parser of the config read",
			setup:   func() *ConfigBuilder { return Setup().RequestBodyParsers(textParser{}) },
			request: sentRequest{method: http.MethodPost, target: "/x", header: http.Header{HeaderContentType: {"application/x-custom"}}, body: []byte("x")},
			want:    RecordedRequest{Method: http.MethodPost, URL: "/x"},
		},
		{
			name:    "the headers the recording names",
			options: []RecordConfigurer{RecordRequestHeaders("accept", "X-Missing", "X-Empty", "Accept")},
			request: sentRequest{target: "/h", header: http.Header{"Accept": {"a", "b"}, "X-Empty": {""}}},
			want: RecordedRequest{
				Method: http.MethodGet, URL: "/h",
				Header: map[string]MatcherSpec{"Accept": {"equal", "a"}, "X-Missing": notPresent, "X-Empty": notPresent},
			},
			match: []sentRequest{{target: "/h", header: http.Header{"Accept": {"a"}}}},
			miss: []sentRequest{
				{target: "/h", header: http.Header{"Accept": {"b"}}},
				{target: "/h", header: http.Header{"Accept": {"a"}, "X-Missing": {"v"}}},
				{target: "/h"},
			},
		},
		{
			name:    "a Content-Type the recording names gives way to the body's",
			options: []RecordConfigurer{RecordRequestHeaders("Content-Type")},
			request: sentRequest{method: http.MethodPost, target: "/j", header: jsonType, body: []byte(`{}`)},
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/j",
				Header: map[string]MatcherSpec{HeaderContentType: contentType("application/json")},
				Body:   MatcherSpec{"eqj", `{}`},
			},
			match: []sentRequest{{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {"application/json"}}, body: []byte(`{}`)}},
		},
		{
			// A form's boundary changes with each client: the Content-Type the recording names matches its media type,
			// though the recording can't read the body, here larger than MaxInlineBody.
			name:    "a Content-Type the recording names, beside a body it can't read",
			options: []RecordConfigurer{RecordRequestHeaders("Content-Type"), RecordMaxInlineBody(16)},
			request: multipartRequest(upload),
			want: RecordedRequest{
				Method: http.MethodPost, URL: "/upload",
				Header: map[string]MatcherSpec{HeaderContentType: contentType(MIMEMultipartForm)},
			},
			match: []sentRequest{multipartRequest(upload)},
		},
		{
			name:    "a header the recording names that isn't UTF-8 text",
			options: []RecordConfigurer{RecordRequestHeaders("X-Bin")},
			request: sentRequest{target: "/h", header: http.Header{"X-Bin": {"\xff"}}},
			want:    RecordedRequest{Method: http.MethodGet, URL: "/h"},
			match:   []sentRequest{{target: "/h", header: http.Header{"X-Bin": {"other"}}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setup := tc.setup
			if setup == nil {
				setup = Setup
			}

			dir := t.TempDir()

			// A mock answers, so what a proxy can't forward, such as a body the server cut, is recorded too.
			m := MustNew(setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(AnyMethod().Reply(OK().PlainText("ok")))
			require.NoError(t, m.StartRecording(append([]RecordConfigurer{RecordDir(dir), RecordIncludeMocked(true)}, tc.options...)...))

			tc.request.send(t, m.URL())
			stopRecording(t, m)

			require.JSONEq(t, jsonOf(t, tc.want), jsonOf(t, onlyMock(t, dir).Request))

			replay := MustNew(setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.json")))
			replay.MustStart()
			t.Cleanup(replay.Close)

			for _, req := range append([]sentRequest{tc.request}, tc.match...) {
				res, body := req.send(t, replay.URL())
				require.Equal(t, http.StatusOK, res.StatusCode, "%s %s %q: %s", req.method, req.target, req.body, body)
			}

			for _, req := range tc.miss {
				res, _ := req.send(t, replay.URL())
				require.Equal(t, StatusNoMatch, res.StatusCode, "%s %s %v %q", req.method, req.target, req.header, req.body)
			}
		})
	}
}

func TestRecordCodec_BodiesTheServerWouldntRead(t *testing.T) {
	// The server reads no such body, so recordings don't meet them: still, what a recording can't read, it doesn't
	// match. A Content-Type it can't write matches any.
	nop := zerolog.Nop()
	c := newRecordCodec(defaultRecordConfig(), nil, &nop)

	call := func(contentType, body string) *recordedCall {
		return &recordedCall{
			method:   http.MethodPost,
			url:      &url.URL{Path: "/r"},
			header:   http.Header{HeaderContentType: {contentType}},
			body:     []byte(body),
			bodyMode: bodyKept,
		}
	}

	tests := []struct {
		name string
		call *recordedCall
		want RecordedRequest
	}{
		{
			name: "a Content-Type that isn't UTF-8 text",
			call: call("text/plain\xff", "hi"),
			want: RecordedRequest{Method: http.MethodPost, URL: "/r", Body: MatcherSpec{"equal", "hi"}},
		},
		{
			name: "a form that doesn't parse",
			call: call(MIMEFormURLEncoded, "a=%zz"),
			want: RecordedRequest{Method: http.MethodPost, URL: "/r"},
		},
		{
			name: "a multipart form without a boundary",
			call: call(MIMEMultipartForm, "--x\r\n"),
			want: RecordedRequest{Method: http.MethodPost, URL: "/r"},
		},
		{
			name: "a multipart form that doesn't parse",
			call: call(MIMEMultipartForm+"; boundary=x", "not a form"),
			want: RecordedRequest{Method: http.MethodPost, URL: "/r"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := c.Request(tc.call, nil)
			require.NoError(t, err)
			require.JSONEq(t, jsonOf(t, tc.want), jsonOf(t, q))
		})
	}

	t.Run("a query neither parsed nor UTF-8 text fails the call", func(t *testing.T) {
		x := &recordedCall{method: http.MethodGet, url: &url.URL{Path: "/r", RawQuery: "a=%zz;\xff"}, header: make(http.Header)}

		_, err := c.Request(x, nil)
		require.ErrorIs(t, err, errQueryNotUTF8)
		require.Equal(t, "GET /r", c.Name(x), "the mock is named after the path")
	})
}

func TestRecording_RecordsTheRequestAsItCame(t *testing.T) {
	// A mapper runs before the answer is recorded, and can change the request: the recording keeps it as it came.
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/r").Reply(OK()).Map(func(rv *RequestValues, _ *Response) error {
		for i := range rv.RawBodyBytes {
			rv.RawBodyBytes[i] = 'X'
		}

		rv.RawRequest.Header.Set("Accept", "changed")

		return nil
	}))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true), RecordRequestHeaders("Accept")))

	do(t, http.MethodPost, m.URL("/r"), http.Header{"Accept": {"a"}, HeaderContentType: {MIMETextPlain}}, []byte("original"))
	stopRecording(t, m)

	request := onlyMock(t, dir).Request
	require.Equal(t, MatcherSpec{"equal", "original"}, request.Body)
	require.Equal(t, MatcherSpec{"equal", "a"}, request.Header["Accept"])
}

func TestRecording_AStatusAMockFileCantHold(t *testing.T) {
	// A status past 599, which Go sends and reads, can't be in a mock file: the call isn't recorded, so the
	// directory still loads, and the recording says why.
	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/profile" {
			w.WriteHeader(999)
		}

		_, _ = io.WriteString(w, "ok")
	})

	m, dir := recordingServer(t, target.URL)

	do(t, http.MethodGet, m.URL("/profile"), nil, nil)
	do(t, http.MethodGet, m.URL("/ok"), nil, nil)

	res, err := m.StopRecording(t.Context())
	require.ErrorContains(t, err, "recorder: status 999 can't be in a mock file")
	require.Len(t, res.Recordings, 1)
	require.Equal(t, "GET /ok", res.Recordings[0].Name)

	_, body := do(t, http.MethodGet, replayServer(t, dir).URL("/ok"), nil, nil)
	require.Equal(t, "ok", string(body))
}

func TestRecording_AHEADKeepsItsContentLength(t *testing.T) {
	// A HEAD answer says the length of a body it doesn't have, which the server can't measure: it is recorded.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "1234")
	})

	m, dir := recordingServer(t, target.URL)

	original, _ := do(t, http.MethodHead, m.URL("/file"), nil, nil)
	require.Equal(t, int64(1234), original.ContentLength)

	stopRecording(t, m)
	require.Equal(t, []string{"1234"}, onlyMock(t, dir).Response.Header["Content-Length"])

	replayed, _ := do(t, http.MethodHead, replayServer(t, dir).URL("/file"), nil, nil)
	require.Equal(t, int64(1234), replayed.ContentLength)
}

func TestGunzip_TheLargestLimit(t *testing.T) {
	// A body larger than the limit is told by one byte more: the largest limit has none more.
	decoded, ok := gunzip(gzipped(t, []byte("text")), math.MaxInt64)
	require.True(t, ok)
	require.Equal(t, "text", string(decoded))
}

func TestRecordCodec_MethodsMatchIgnoringCase(t *testing.T) {
	// A method matches ignoring case, so calls that differ in case are one request.
	nop := zerolog.Nop()
	c := newRecordCodec(defaultRecordConfig(), nil, &nop)

	x := &recordedCall{method: "purge", url: &url.URL{Path: "/p"}, header: make(http.Header)}

	q, err := c.Request(x, nil)
	require.NoError(t, err)
	require.Equal(t, "PURGE", q.Method)
	require.Equal(t, "PURGE /p", c.Name(x))
}

func TestRecording_RequestsGroupByWhatTheyMatch(t *testing.T) {
	// Calls a recording can't tell apart are one mock, whose responses play in turn; calls it can, are two.
	tests := []struct {
		name     string
		requests []sentRequest
		mocks    int
	}{
		{"query parameters in another order", []sentRequest{{target: "/a?b=2&a=1"}, {target: "/a?a=1&b=2"}}, 1},
		{"the values of a parameter in another order", []sentRequest{{target: "/a?a=1&a=2"}, {target: "/a?a=2&a=1"}}, 2},
		{"a letter escaped", []sentRequest{{target: "/%61"}, {target: "/a"}}, 1},
		{"a slash escaped", []sentRequest{{target: "/a%2Fb"}, {target: "/a/b"}}, 2},
		{"JSON written another way", []sentRequest{
			{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {MIMEApplicationJSON}}, body: []byte(`{"a": 1, "b": [1]}`)},
			{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {MIMEApplicationJSON}}, body: []byte(`{"b":[1],"a":1}`)},
		}, 1},
		{"JSON lists in another order", []sentRequest{
			{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {MIMEApplicationJSON}}, body: []byte(`[1, 2]`)},
			{method: http.MethodPost, target: "/j", header: http.Header{HeaderContentType: {MIMEApplicationJSON}}, body: []byte(`[2, 1]`)},
		}, 2},
	}

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, dir := recordingServer(t, target.URL)

			for _, req := range tc.requests {
				req.send(t, m.URL())
			}

			res := stopRecording(t, m)
			require.Len(t, res.Recordings, tc.mocks)
			require.Len(t, recordedMocks(t, dir), tc.mocks)
		})
	}
}

func TestRecording_ResponseSection(t *testing.T) {
	// A recording answers as the client was answered: the same status, headers, trailers and body, with what the
	// server sets itself, such as Date, left to the server that plays it back.
	ten := bytes.Repeat([]byte("0"), 10<<10)

	write := func(status int, header http.Header, body []byte) ReplierFactory {
		return Handler(func(w http.ResponseWriter, _ *http.Request) {
			maps.Copy(w.Header(), header)

			w.WriteHeader(status)
			_, _ = w.Write(body)
		})
	}

	tests := []struct {
		name    string
		options []RecordConfigurer
		method  string
		reply   ReplierFactory
		want    RecordedResponse
		file    []byte   // the content of the body file
		left    []string // headers the recording leaves out, which the replay doesn't send
	}{
		{
			name:    "a status and every header value, in order",
			options: []RecordConfigurer{RecordExcludeResponseHeaders()},
			reply: write(http.StatusCreated, http.Header{
				HeaderContentType: {MIMETextPlain}, "Set-Cookie": {"a=1", "b=2"}, "X-Multi": {"2", "1"},
			}, []byte("hi")),
			want: RecordedResponse{
				Status: http.StatusCreated,
				Header: map[string][]string{HeaderContentType: {MIMETextPlain}, "Set-Cookie": {"a=1", "b=2"}, "X-Multi": {"2", "1"}},
				Body:   "hi",
			},
		},
		{
			name:  "a header value that isn't UTF-8 text is left out",
			reply: write(http.StatusOK, http.Header{"X-Bin": {"\xff"}, "X-Keep": {"1"}}, nil),
			want:  RecordedResponse{Status: http.StatusOK, Header: map[string][]string{"X-Keep": {"1"}}},
			left:  []string{"X-Bin"},
		},
		{
			name:  "a redirect",
			reply: NewReply().Status(http.StatusFound).Header("Location", "/there"),
			want:  RecordedResponse{Status: http.StatusFound, Header: map[string][]string{"Location": {"/there"}}},
		},
		{
			name:  "headers the server sets",
			reply: write(http.StatusOK, http.Header{"Date": {"today"}, HeaderContentLength: {"2"}, "Keep-Alive": {"timeout=5"}}, []byte("hi")),
			want:  RecordedResponse{Status: http.StatusOK, Body: "hi"},
		},
		{
			name:    "headers the recording leaves out",
			options: []RecordConfigurer{RecordExcludeResponseHeaders("set-cookie")},
			reply:   write(http.StatusOK, http.Header{"Set-Cookie": {"a=1"}, "X-Keep": {"1"}}, nil),
			want:    RecordedResponse{Status: http.StatusOK, Header: map[string][]string{"X-Keep": {"1"}}},
			left:    []string{"Set-Cookie"},
		},
		{
			// A cookie is the service's session: mock files, often committed, don't keep it unless asked.
			name:  "a cookie is left out by default",
			reply: write(http.StatusOK, http.Header{"Set-Cookie": {"session=secret"}, "X-Keep": {"1"}}, nil),
			want:  RecordedResponse{Status: http.StatusOK, Header: map[string][]string{"X-Keep": {"1"}}},
			left:  []string{"Set-Cookie"},
		},
		{
			name:    "headers to leave out replace the default",
			options: []RecordConfigurer{RecordExcludeResponseHeaders("x-drop")},
			reply:   write(http.StatusOK, http.Header{"Set-Cookie": {"a=1"}, "X-Drop": {"1"}}, nil),
			want:    RecordedResponse{Status: http.StatusOK, Header: map[string][]string{"Set-Cookie": {"a=1"}}},
			left:    []string{"X-Drop"},
		},
		{
			name:  "a gzip body is recorded decoded",
			reply: OK().Header(HeaderContentType, MIMEApplicationJSON).Body([]byte(`{"a":1}`)).Gzip(),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentType: {MIMEApplicationJSON}}, Body: `{"a":1}`, Encoding: "gzip",
			},
		},
		{
			name:  "gzip in upper case",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"GZIP"}}, gzipped(t, []byte("text"))),
			want:  RecordedResponse{Status: http.StatusOK, Body: "text", Encoding: "gzip"},
		},
		{
			name:  "a gzip stream that doesn't decode is kept as sent",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}}, []byte("not gzip at all")),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentEncoding: {"gzip"}},
				BodyFile: bodyFileName("GET-r", []byte("not gzip at all"), ".bin"),
			},
			file: []byte("not gzip at all"),
		},
		{
			name:    "a gzip body larger than MaxInlineBody once decoded is kept as sent",
			options: []RecordConfigurer{RecordMaxInlineBody(1 << 10)},
			reply:   write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}}, gzipped(t, ten)),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentEncoding: {"gzip"}},
				BodyFile: bodyFileName("GET-r", gzipped(t, ten), ".bin"),
			},
			file: gzipped(t, ten),
		},
		{
			name:  "two encodings are kept as sent",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip, br"}}, []byte{1, 2}),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentEncoding: {"gzip, br"}},
				BodyFile: bodyFileName("GET-r", []byte{1, 2}, ".bin"),
			},
			file: []byte{1, 2},
		},
		{
			name:  "another encoding is kept as sent",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"br"}, HeaderContentType: {MIMEApplicationJSON}}, []byte("{}")),
			want: RecordedResponse{
				Status:   http.StatusOK,
				Header:   map[string][]string{HeaderContentEncoding: {"br"}, HeaderContentType: {MIMEApplicationJSON}},
				BodyFile: bodyFileName("GET-r", []byte("{}"), ".bin"),
			},
			file: []byte("{}"),
		},
		{
			name:  "identity is no encoding",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"identity"}}, []byte("plain")),
			want:  RecordedResponse{Status: http.StatusOK, Header: map[string][]string{HeaderContentEncoding: {"identity"}}, Body: "plain"},
		},
		{
			name:  "a body that isn't text goes to a file named after its type",
			reply: write(http.StatusOK, http.Header{HeaderContentType: {"image/png"}}, []byte("\x89PNG\x00")),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentType: {"image/png"}},
				BodyFile: bodyFileName("GET-r", []byte("\x89PNG\x00"), ".png"),
			},
			file: []byte("\x89PNG\x00"),
		},
		{
			name:    "a text body larger than MaxInlineBody goes to a file as it streams",
			options: []RecordConfigurer{RecordMaxInlineBody(4)},
			reply:   write(http.StatusOK, http.Header{HeaderContentType: {MIMETextPlain}}, []byte("12345")),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentType: {MIMETextPlain}},
				BodyFile: bodyFileName("GET-r", []byte("12345"), ".txt"),
			},
			file: []byte("12345"),
		},
		{
			name:    "a gzip body that went to a file is kept as sent",
			options: []RecordConfigurer{RecordMaxInlineBody(8)},
			reply:   write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}, HeaderContentType: {MIMETextPlain}}, gzipped(t, ten)),
			want: RecordedResponse{
				Status:   http.StatusOK,
				Header:   map[string][]string{HeaderContentEncoding: {"gzip"}, HeaderContentType: {MIMETextPlain}},
				BodyFile: bodyFileName("GET-r", gzipped(t, ten), ".bin"),
			},
			file: gzipped(t, ten),
		},
		{
			name:  "a gzip body with a strong ETag is kept as sent",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}, "Etag": {`"v1"`}}, gzipped(t, []byte("text"))),
			want: RecordedResponse{
				Status:   http.StatusOK,
				Header:   map[string][]string{HeaderContentEncoding: {"gzip"}, "Etag": {`"v1"`}},
				BodyFile: bodyFileName("GET-r", gzipped(t, []byte("text")), ".bin"),
			},
			file: gzipped(t, []byte("text")),
		},
		{
			name:  "a gzip body with a weak ETag is recorded decoded",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}, "Etag": {`W/"v1"`}}, gzipped(t, []byte("text"))),
			want:  RecordedResponse{Status: http.StatusOK, Header: map[string][]string{"Etag": {`W/"v1"`}}, Body: "text", Encoding: "gzip"},
		},
		{
			name:    "a gzip body whose ETag the recording leaves out is recorded decoded",
			options: []RecordConfigurer{RecordExcludeResponseHeaders("etag")},
			reply:   write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}, "Etag": {`"v1"`}}, gzipped(t, []byte("text"))),
			want:    RecordedResponse{Status: http.StatusOK, Body: "text", Encoding: "gzip"},
			left:    []string{"Etag"},
		},
		{
			name:  "a gzip body with a digest is kept as sent",
			reply: write(http.StatusOK, http.Header{HeaderContentEncoding: {"gzip"}, "Content-Md5": {"q1w2e3=="}}, gzipped(t, []byte("text"))),
			want: RecordedResponse{
				Status:   http.StatusOK,
				Header:   map[string][]string{HeaderContentEncoding: {"gzip"}, "Content-Md5": {"q1w2e3=="}},
				BodyFile: bodyFileName("GET-r", gzipped(t, []byte("text")), ".bin"),
			},
			file: gzipped(t, []byte("text")),
		},
		{
			name: "a gzip body with a digest trailer is kept as sent",
			reply: Handler(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(HeaderTrailer, "Content-Digest")
				w.Header().Set(HeaderContentEncoding, "gzip")

				_, _ = w.Write(gzipped(t, []byte("text")))

				w.Header().Set("Content-Digest", "sha-256=:q1w2e3=:")
			}),
			want: RecordedResponse{
				Status:   http.StatusOK,
				Header:   map[string][]string{HeaderContentEncoding: {"gzip"}},
				Trailer:  map[string][]string{"Content-Digest": {"sha-256=:q1w2e3=:"}},
				BodyFile: bodyFileName("GET-r", gzipped(t, []byte("text")), ".bin"),
			},
			file: gzipped(t, []byte("text")),
		},
		{
			name:  "a range of a gzip body is kept as sent",
			reply: write(http.StatusPartialContent, http.Header{HeaderContentEncoding: {"gzip"}, "Content-Range": {"bytes 0-23/24"}}, gzipped(t, []byte("text"))),
			want: RecordedResponse{
				Status:   http.StatusPartialContent,
				Header:   map[string][]string{HeaderContentEncoding: {"gzip"}, "Content-Range": {"bytes 0-23/24"}},
				BodyFile: bodyFileName("GET-r", gzipped(t, []byte("text")), ".bin"),
			},
			file: gzipped(t, []byte("text")),
		},
		{
			name:    "every body to a file",
			options: []RecordConfigurer{RecordBodyFiles(true)},
			reply:   OK().Header(HeaderContentType, MIMEApplicationJSON).Body([]byte(`{"a":1}`)),
			want: RecordedResponse{
				Status: http.StatusOK, Header: map[string][]string{HeaderContentType: {MIMEApplicationJSON}},
				BodyFile: bodyFileName("GET-r", []byte(`{"a":1}`), ".json"),
			},
			file: []byte(`{"a":1}`),
		},
		{
			name:  "no body",
			reply: OK(),
			want:  RecordedResponse{Status: http.StatusOK},
		},
		{
			name:  "a status without a body",
			reply: NewReply().Status(http.StatusNoContent).PlainText("dropped"),
			want:  RecordedResponse{Status: http.StatusNoContent, Header: map[string][]string{HeaderContentType: {MIMETextPlain}}},
		},
		{
			name:   "a HEAD",
			method: http.MethodHead,
			reply:  OK().PlainText("dropped"),
			want:   RecordedResponse{Status: http.StatusOK, Header: map[string][]string{HeaderContentType: {MIMETextPlain}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(AnyMethod().Reply(tc.reply))
			require.NoError(t, m.StartRecording(append([]RecordConfigurer{RecordDir(dir), RecordIncludeMocked(true)}, tc.options...)...))

			method := cmp.Or(tc.method, http.MethodGet)
			original, originalBody := do(t, method, m.URL("/r"), nil, nil)
			stopRecording(t, m)

			mock := onlyMock(t, dir)
			require.NotNil(t, mock.Response)
			require.JSONEq(t, jsonOf(t, tc.want), jsonOf(t, *mock.Response))

			if tc.file != nil {
				b, err := os.ReadFile(filepath.Join(dir, tc.want.BodyFile))
				require.NoError(t, err)
				require.Equal(t, tc.file, b)
			}

			for _, name := range tc.left {
				original.Header.Del(name)
			}

			replayed, replayedBody := do(t, method, replayServer(t, dir).URL("/r"), nil, nil)
			requireSameAnswer(t, original, replayed, originalBody, replayedBody)

			if tc.file != nil {
				require.Equal(t, originalBody, replayedBody, "a body file plays back the bytes as sent")
			}
		})
	}
}

func TestRecording_HidesTheValuesOfRedactedHeaders(t *testing.T) {
	// The headers the server's config redacts hold secrets, such as tokens: the request log hides their values, and
	// so do the mock files, often committed. A redacted request header still matches, by its presence.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Token", "TOKEN-SECRET")
		_, _ = io.WriteString(w, "ok")
	})

	m := MustNew(Setup().Silent().RedactHeader("Authorization", "x-token"))
	m.MustStart()
	t.Cleanup(m.Close)

	dir := t.TempDir()
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL), RecordRequestHeaders("Authorization")))

	do(t, http.MethodGet, m.URL("/me"), http.Header{"Authorization": {"Bearer AUTH-SECRET"}}, nil)

	data, err := os.ReadFile(stopRecording(t, m).Recordings[0].File)
	require.NoError(t, err)
	require.NotContains(t, string(data), "SECRET")

	mock := onlyMock(t, dir)
	require.Equal(t, []string{_redacted}, mock.Response.Header["X-Token"])
	require.Equal(t, MatcherSpec{"present"}, mock.Request.Header["Authorization"])

	replay := replayServer(t, dir)

	_, body := do(t, http.MethodGet, replay.URL("/me"), http.Header{"Authorization": {"Bearer another"}}, nil)
	require.Equal(t, "ok", string(body), "any value matches")

	res, _ := do(t, http.MethodGet, replay.URL("/me"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode, "no header doesn't")
}

// requireSameAnswer requires a replayed answer to be the original one: the same status, headers, trailers and body,
// but for the headers a server sets itself. A gzip body may be compressed another way, so it compares decoded.
func requireSameAnswer(t require.TestingT, original, replayed *http.Response, originalBody, replayedBody []byte) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}

	require.Equal(t, original.StatusCode, replayed.StatusCode, "status")
	require.Equal(t, answerHeader(original.Header), answerHeader(replayed.Header), "headers")
	require.Equal(t, original.Trailer, replayed.Trailer, "trailers")

	if strings.EqualFold(original.Header.Get(HeaderContentEncoding), "gzip") {
		if decoded, ok := gunzip(originalBody, 1<<30); ok {
			replayedDecoded, ok := gunzip(replayedBody, 1<<30)
			require.True(t, ok, "the replayed body is gzip too")
			require.Equal(t, decoded, replayedDecoded, "body")

			return
		}
	}

	require.Equal(t, originalBody, replayedBody, "body")
}

// answerHeader returns the headers of an answer but those a server sets itself, and those of the connection. The debug
// headers say which server or mock answered, not what the answer was. A gzip encoding is written in lower case when it
// is played back.
func answerHeader(h http.Header) http.Header {
	h = h.Clone()
	h.Del("Date")
	h.Del(HeaderContentLength)

	for _, name := range slices.Concat(_forbiddenHeaders, _debugHeaders) {
		h.Del(name)
	}

	if strings.EqualFold(h.Get(HeaderContentEncoding), "gzip") {
		h.Set(HeaderContentEncoding, "gzip")
	}

	return h
}
