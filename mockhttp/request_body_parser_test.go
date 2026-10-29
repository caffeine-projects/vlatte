package mockhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/match"
)

func TestRequestBodyParsers_CanParse(t *testing.T) {
	formParser := &formURLEncodedParser{}
	textParser := &plainTextParser{}
	multipartParser := &multipartFormParser{}
	noop := &noopParser{}

	newReq := func(header map[string]string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, "https://localhost:8080", nil)
		require.NoError(t, err)

		for k, v := range header {
			req.Header.Add(k, v)
		}

		return req
	}

	testCases := []struct {
		name        string
		contentType string
		parser      RequestBodyParser
		req         *http.Request
		expected    bool
	}{
		{"Form: can parse application/form-url-encoded", MIMEFormURLEncoded, formParser, newReq(map[string]string{HeaderContentType: MIMEFormURLEncoded}), true},
		{"Form: can parse application/form-url-encoded; charset=UTF-8", MIMEFormURLEncodedCharsetUTF8, formParser, newReq(map[string]string{HeaderContentType: MIMEFormURLEncodedCharsetUTF8}), true},
		{"Form: should not parse", MIMEApplicationJSON, formParser, newReq(map[string]string{HeaderContentType: MIMEApplicationJSON}), false},

		{"Text: can parse text/plain", MIMETextPlain, textParser, newReq(map[string]string{HeaderContentType: MIMETextPlain}), true},
		{"Text: can parse text/plain; charset=UTF-8", MIMETextPlainCharsetUTF8, textParser, newReq(map[string]string{HeaderContentType: MIMETextPlainCharsetUTF8}), true},
		{"Text: should parse JSON as text", MIMEApplicationJSON, textParser, newReq(map[string]string{HeaderContentType: MIMEApplicationJSON}), true},

		{"Noop: should always parse", MIMETextPlain, noop, newReq(map[string]string{HeaderContentType: MIMETextPlain}), true},

		// Parsers compare media types: a multipart boundary may hold one, quoted, and a type may only start alike.
		{"Form: not a multipart form whose boundary names it", `multipart/form-data; boundary="application/x-www-form-urlencoded"`, formParser, newReq(nil), false},
		{"Text: not a multipart form whose boundary names JSON", `multipart/form-data; boundary="application/json"`, textParser, newReq(nil), false},
		{"Text: not a multipart form whose boundary names text", `multipart/form-data; boundary="text/plain"`, textParser, newReq(nil), false},
		{"Multipart: a boundary that names a media type", `multipart/form-data; boundary="text/plain"`, multipartParser, newReq(nil), true},
		{"Text: media types ignore case", "Text/Plain; charset=UTF-8", textParser, newReq(nil), true},
		{"Text: JSON in any case", "Application/JSON", textParser, newReq(nil), true},
		{"Text: not a type that only starts alike", "text/plaintext", textParser, newReq(nil), false},
		{"Text: not a JSON lookalike", "application/json-patch+json", textParser, newReq(nil), false},
		{"Form: a malformed parameter keeps the media type", MIMEFormURLEncoded + "; charset", formParser, newReq(nil), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.parser.CanParse(tc.contentType, tc.req))
		})
	}
}

func TestRequestBodyParsers_Parse(t *testing.T) {
	formParser := &formURLEncodedParser{}
	textParser := &plainTextParser{}
	noop := &noopParser{}

	req, err := http.NewRequest(http.MethodPost, "https://localhost:8080", nil)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		parser   RequestBodyParser
		req      *http.Request
		body     []byte
		expected any
	}{
		{"form", formParser, func(r *http.Request) *http.Request {
			req, err := http.NewRequest(http.MethodPost, "https://localhost:8080", strings.NewReader("test=ok"))
			req.Header.Add(HeaderContentType, MIMEFormURLEncoded)
			require.NoError(t, err)

			return req
		}(req), []byte(`test=ok`), url.Values{"test": []string{"ok"}}},
		{"text", textParser, req, []byte(`hello world`), "hello world"},
		{"noop", noop, req, []byte(`hello world`), []byte(`hello world`)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.parser.Parse(tc.body, tc.req)

			require.NoError(t, err)
			require.Equal(t, tc.expected, b)
		})
	}
}

func TestRequestBodyParser_Limit(t *testing.T) {
	m := MustNew(Setup().MaxBodyParsingLimit(5))
	m.MustMock(Postf("/test").Body(match.Eq("hello")).Reply(Func(func(w http.ResponseWriter, rv *RequestValues) (*Response, error) {
		require.Equal(t, "hello", rv.ParsedBodyData.(string))
		return &Response{StatusCode: http.StatusOK, Body: []byte(rv.ParsedBodyData.(string))}, nil
	})))
	m.MustStart()

	defer m.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodPost, m.URL("/test"), strings.NewReader("hello world"))
	req.Header.Add(HeaderContentType, MIMETextPlainCharsetUTF8)

	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestRequestBodyParser_ForwardsTheWholeBodyPastTheLimit(t *testing.T) {
	// #41: the server kept only the MaxBodyParsingLimit bytes it parsed, so replies that forward the request sent a
	// cut body: the transport refused one with a Content-Length, and a chunked one reached the target short. The limit
	// bounds what the matchers see, not what is forwarded.
	const body = "0123456789"

	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().MaxBodyParsingLimit(4).Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	// The matchers see the first 4 bytes only: a mock that answers saw the cut body.
	m.MustMock(
		Postf("/forward").Body(match.Eq("0123")).Reply(From(target.URL)),
		Postf("/echo").Body(match.Eq("0123")).Reply(Echo()),
		Postf("/form").Reply(From(target.URL)),
	)

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	viaProxy := &http.Client{Transport: transport}

	send := func(t *testing.T, client *http.Client, url string, chunked bool) (int, string) {
		t.Helper()

		var r io.Reader = strings.NewReader(body)
		if chunked {
			// A reader of unknown length goes chunked.
			r = io.MultiReader(r)
		}

		req, err := http.NewRequest(http.MethodPost, url, r)
		require.NoError(t, err)
		req.Header.Set(HeaderContentType, MIMETextPlain)

		res, err := client.Do(req)
		require.NoError(t, err)

		b, err := readAll(res)
		require.NoError(t, err)

		return res.StatusCode, string(b)
	}

	for _, chunked := range []bool{false, true} {
		name := "with a Content-Length"
		if chunked {
			name = "chunked"
		}

		t.Run("From, "+name, func(t *testing.T) {
			status, _ := send(t, http.DefaultClient, m.URL("/forward"), chunked)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, body, <-got)
		})

		t.Run("forward proxy, "+name, func(t *testing.T) {
			status, _ := send(t, viaProxy, target.URL+"/upload", chunked)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, body, <-got)
		})
	}

	t.Run("Echo, with a Content-Length", func(t *testing.T) {
		status, echoed := send(t, http.DefaultClient, m.URL("/echo"), false)
		require.Equal(t, http.StatusOK, status)
		require.True(t, strings.HasSuffix(echoed, "\r\n\r\n"+body), "the echo ends with the whole body: %q", echoed)
	})

	t.Run("From, a multipart form", func(t *testing.T) {
		// The multipart parser refuses a form the limit cut: the form goes on whole all the same.
		contentType, form := formModel{fields: []formField{{name: "a", value: "1"}}}.encode(t)

		res, _ := post(t, m.URL("/form"), contentType, form)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, string(form), <-got)
	})
}

// replacingParser parses its media type by replacing the request's body with body, as a parser that decodes it would.
type replacingParser struct {
	body string
}

func (p replacingParser) CanParse(content string, _ *http.Request) bool {
	return content == "application/x-replaced"
}

func (p replacingParser) Parse(_ []byte, r *http.Request) (any, error) {
	r.Body = io.NopCloser(strings.NewReader(p.body))
	return p.body, nil
}

// failingParser fails to parse its media type.
type failingParser struct{}

func (failingParser) CanParse(content string, _ *http.Request) bool {
	return content == "application/x-broken"
}

func (failingParser) Parse([]byte, *http.Request) (any, error) { return nil, errors.New("bad body") }

func TestRequestBodyParser_AFailingParserWarnsNamingWhatItWasDoing(t *testing.T) {
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).RequestBodyParsers(failingParser{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/x").Reply(OK()))

	res, _ := post(t, m.URL("/x"), "application/x-broken", []byte("x"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, logs.String(), "request_body_parser: parsing the body with mockhttp.failingParser: bad body")
}

func TestRequestBodyParser_KeepsTheBodyAParserReplaced(t *testing.T) {
	// A parser can replace the request's body, such as with the body it decoded: replies that forward the request, such
	// as Echo, send it. Only a body the parsers left as it was gets the rest the server didn't read put back.
	m := MustNew(Setup().Silent().RequestBodyParsers(replacingParser{body: "xyz"}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/echo").Reply(Echo()))

	res, echoed := post(t, m.URL("/echo"), "application/x-replaced", []byte("abc"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.True(t, strings.HasSuffix(string(echoed), "\r\n\r\nxyz"), "the echo ends with the parser's body: %q", echoed)
}

func TestParseURL_SegmentsExcludeQuery(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "http://localhost/a/b?x=1", nil)
	require.NoError(t, err)

	u, segments := parseURL(r, "http://localhost")

	// Path segments come from the path only; the query stays in the full URL.
	require.Equal(t, []string{"a", "b"}, segments)
	require.Equal(t, "x=1", u.RawQuery)
}

func TestParseURL_KeepsThePathAsSent(t *testing.T) {
	// Decoded, an escaped "?" or "#" would start a query or a fragment, an escaped "/" would split its segment, and an
	// escaped "%" would be decoded twice.
	tests := []struct {
		giveMethod   string
		giveTarget   string
		wantURL      string
		wantPath     string
		wantQuery    string
		wantSegments []string
	}{
		{
			giveMethod: http.MethodGet, giveTarget: "/test%3Fq=none",
			wantURL: "http://localhost/test%3Fq=none", wantPath: "/test?q=none", wantSegments: []string{"test?q=none"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/test%23contacts",
			wantURL: "http://localhost/test%23contacts", wantPath: "/test#contacts", wantSegments: []string{"test#contacts"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/a/b%2Fc",
			wantURL: "http://localhost/a/b%2Fc", wantPath: "/a/b/c", wantSegments: []string{"a", "b/c"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/files/a%20b",
			wantURL: "http://localhost/files/a%20b", wantPath: "/files/a b", wantSegments: []string{"files", "a b"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/a%2541",
			wantURL: "http://localhost/a%2541", wantPath: "/a%41", wantSegments: []string{"a%41"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/50%25",
			wantURL: "http://localhost/50%25", wantPath: "/50%", wantSegments: []string{"50%"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "*",
			wantURL: "http://localhost/*", wantPath: "*", wantSegments: []string{"*"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "/a/b?x=1",
			wantURL: "http://localhost/a/b?x=1", wantPath: "/a/b", wantQuery: "x=1", wantSegments: []string{"a", "b"},
		},
		// An empty query is no query.
		{
			giveMethod: http.MethodGet, giveTarget: "/test?",
			wantURL: "http://localhost/test", wantPath: "/test", wantSegments: []string{"test"},
		},
		// A CONNECT target is a host: the URL is the server's.
		{
			giveMethod: http.MethodConnect, giveTarget: "example.com:443",
			wantURL: "http://localhost", wantSegments: []string{},
		},
		// A request in absolute form, as a forward proxy gets, and an intercepted one names its scheme and host: the URL
		// keeps them, without the scheme's default port.
		{
			giveMethod: http.MethodGet, giveTarget: "http://api.local/x?a=1",
			wantURL: "http://api.local/x?a=1", wantPath: "/x", wantQuery: "a=1", wantSegments: []string{"x"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "https://api.local:443/x",
			wantURL: "https://api.local/x", wantPath: "/x", wantSegments: []string{"x"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "http://api.local:80/x",
			wantURL: "http://api.local/x", wantPath: "/x", wantSegments: []string{"x"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "http://api.local:8443/x",
			wantURL: "http://api.local:8443/x", wantPath: "/x", wantSegments: []string{"x"},
		},
		// A port is the default of its own scheme only.
		{
			giveMethod: http.MethodGet, giveTarget: "http://api.local:443/x",
			wantURL: "http://api.local:443/x", wantPath: "/x", wantSegments: []string{"x"},
		},
		{
			giveMethod: http.MethodGet, giveTarget: "https://[::1]:443/x",
			wantURL: "https://[::1]/x", wantPath: "/x", wantSegments: []string{"x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.giveMethod+" "+tt.giveTarget, func(t *testing.T) {
			u, segments := parseURL(httptest.NewRequest(tt.giveMethod, tt.giveTarget, nil), "http://localhost")

			require.Equal(t, tt.wantURL, u.String())
			require.Equal(t, tt.wantPath, u.Path)
			require.Equal(t, tt.wantQuery, u.RawQuery)
			require.Equal(t, tt.wantSegments, segments)
		})
	}
}

func TestWithFragment_MovesTheFragmentOutOfThePathAndQuery(t *testing.T) {
	// Go's server reads no fragment: it leaves a "#" and what follows in r.URL's path, or in its query, where q would
	// be "1#contacts".
	tests := []struct {
		giveTarget   string
		wantPath     string
		wantQuery    string
		wantFragment string
	}{
		{giveTarget: "/test#contacts", wantPath: "/test", wantFragment: "contacts"},
		{giveTarget: "/test?q=1#contacts", wantPath: "/test", wantQuery: "q=1", wantFragment: "contacts"},
		// The first "#" starts the fragment, even before a "?".
		{giveTarget: "/a#b?c", wantPath: "/a", wantFragment: "b?c"},
		// A fragment that doesn't decode is left out, and the query still ends at its "#".
		{giveTarget: "/t?q=1#%zz", wantPath: "/t", wantQuery: "q=1"},
		{giveTarget: "/t#a%20b", wantPath: "/t", wantFragment: "a b"},
		// Parsed as any URL, rather than as a request target, "//double" would be a host.
		{giveTarget: "//double#f", wantPath: "//double", wantFragment: "f"},
		{giveTarget: "http://example.com/p?q=1#f", wantPath: "/p", wantQuery: "q=1", wantFragment: "f"},
	}

	for _, tt := range tests {
		t.Run(tt.giveTarget, func(t *testing.T) {
			got := passThrough(t, httptest.NewRequest(http.MethodGet, tt.giveTarget, nil))

			require.Equal(t, tt.wantPath, got.URL.Path)
			require.Equal(t, tt.wantQuery, got.URL.RawQuery)
			require.Equal(t, tt.wantFragment, got.URL.Fragment)
			require.Equal(t, tt.giveTarget, got.RequestURI)
		})
	}
}

func TestWithFragment_PassesOnRequestsItLeavesAlone(t *testing.T) {
	// A request with no fragment to move reaches the handlers as it came: the same request.
	connect := httptest.NewRequest(http.MethodConnect, "example.com:443", nil)
	// A CONNECT target is a host, so a "#" in it starts no fragment. HTTP/2 doesn't check the authority it comes in.
	connect.RequestURI = "example.com:443#x"

	// The server rejects a target that doesn't parse. Should one come anyway, its fragment is not worth dropping it.
	unparsed := httptest.NewRequest(http.MethodGet, "/a", nil)
	unparsed.RequestURI = "/a%zz#x"

	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/test%23contacts", nil),
		httptest.NewRequest(http.MethodGet, "/a/b?x=1", nil),
		connect,
		unparsed,
	} {
		t.Run(r.RequestURI, func(t *testing.T) {
			require.Same(t, r, passThrough(t, r))
		})
	}
}

// passThrough returns the request withFragment hands to the next handler.
func passThrough(t *testing.T, r *http.Request) *http.Request {
	t.Helper()

	var got *http.Request
	withFragment(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r })).
		ServeHTTP(httptest.NewRecorder(), r)
	require.NotNil(t, got)

	return got
}

func TestParseURL_KeepsTheFragment(t *testing.T) {
	// The server never fills in r.URL.Fragment: withFragment does, from the request target.
	r, err := http.NewRequest(http.MethodGet, "http://localhost/t#top", nil)
	require.NoError(t, err)

	u, _ := parseURL(r, "http://localhost")

	require.Equal(t, "top", u.Fragment)
	require.Equal(t, "http://localhost/t#top", u.String())
}

func TestForms_FormFieldReadsMultipartFields(t *testing.T) {
	// Decision 2: FormField reads the text fields of the body's form, urlencoded or multipart, as
	// http.Request.PostFormValue does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/form").FormField("title", match.StrictEqual("Holiday")).Reply(OK()))

	holiday, holidayBody := formModel{fields: []formField{{name: "title", value: "Holiday"}}}.encode(t)
	other, otherBody := formModel{fields: []formField{{name: "title", value: "Other"}}}.encode(t)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		status      int
	}{
		{"multipart", holiday, holidayBody, http.StatusOK},
		{"urlencoded", MIMEFormURLEncoded, []byte("title=Holiday"), http.StatusOK},
		{"multipart, another value", other, otherBody, StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := post(t, m.URL("/form"), tc.contentType, tc.body)
			require.Equal(t, tc.status, res.StatusCode)
		})
	}

	scoped.AssertNumberOfCalls(t, 2)
}

func TestForms_TheQueryIsNotAMultipartField(t *testing.T) {
	// Like TestMockFile_FormFieldsComeFromTheBody: http.Request.Form would mix the query in; FormField reads only
	// the body's form.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/form").FormField("a", match.StrictEqual("1")).Reply(OK()))

	contentType, body := formModel{fields: []formField{{name: "b", value: "2"}}}.encode(t)

	res, _ := post(t, m.URL("/form?a=1"), contentType, body)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestForms_LineBreaksInNamesArriveEscaped(t *testing.T) {
	// genFormName leaves CR and LF out: Go's multipart writer escapes them as %0D and %0A, and nothing unescapes
	// them, so the server sees the escaped name.
	contentType, body := formModel{fields: []formField{{name: "a\r\nb", value: "1"}}}.encode(t)

	r, err := stdlibForm(contentType, body)
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a%0D%0Ab"))
}

func TestForms_ForwardAndEchoSendTheWholeBody(t *testing.T) {
	// D6: parsing a form reads r.Body to the end, and the proxy, forward and echo replies send r.Body, so they sent
	// an empty body. The server puts the whole body back once the parsers are done.
	got := make(chan []byte, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/forward").Reply(From(target.URL)))
	m.MustMock(Postf("/echo").Reply(Echo()))

	// The proxy sends a request that no mock matches to its own URL.
	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	viaProxy := &http.Client{Transport: transport}

	multipartType, multipartBody := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.bin", body: []byte("\x00\r\n--")}},
	}.encode(t)

	bodies := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"urlencoded", MIMEFormURLEncoded, []byte("a=1&b=2")},
		{"multipart", multipartType, multipartBody},
	}

	for _, b := range bodies {
		t.Run(b.name+" forward", func(t *testing.T) {
			res, _ := post(t, m.URL("/forward"), b.contentType, b.body)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, b.body, <-got)
		})

		t.Run(b.name+" echo", func(t *testing.T) {
			res, echoed := post(t, m.URL("/echo"), b.contentType, b.body)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.True(t, bytes.HasSuffix(echoed, b.body), "the echo ends with the whole body: %q", echoed)
		})

		t.Run(b.name+" proxy", func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, target.URL+"/upload", bytes.NewReader(b.body))
			require.NoError(t, err)
			req.Header.Set(HeaderContentType, b.contentType)

			res, err := viaProxy.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, b.body, <-got)
		})
	}
}

func TestForms_ACutFormKeepsNoField(t *testing.T) {
	// D8: MaxBodyParsingLimit cuts a body without a word. Go's ReadForm takes a body cut right after a delimiter
	// line, or inside the headers of a part, for a whole one, and keeps the fields before the cut; one cut inside a
	// part's content fails. Either way a cut form keeps no field, so no form mock matches, and a warning says why.
	contentType, body := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.bin", body: bytes.Repeat([]byte("x"), 1024)}},
	}.encode(t)

	second := bytes.Index(body[2:], []byte("--")) + 2 // the delimiter line before the file part

	cuts := []struct {
		name string
		at   int
	}{
		{"after a delimiter line", bytes.Index(body[second:], []byte("\r\n")) + second + 2},
		{"inside the headers of a part", bytes.Index(body, []byte("filename"))},
		{"inside the content of a part", len(body) - 100},
	}

	for _, cut := range cuts {
		t.Run(cut.name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).MaxBodyParsingLimit(int64(cut.at)))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Postf("/form").FormField("a", match.StrictEqual("1")).Reply(OK()))

			res, _ := post(t, m.URL("/form"), contentType, body)
			require.Equal(t, StatusNoMatch, res.StatusCode)
			require.Contains(t, logs.String(), "request_body_parser")
		})
	}

	// The cuts ReadForm takes for whole bodies.
	r, err := stdlibForm(contentType, body[:cuts[0].at])
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a"), "Go keeps the field before a cut after a delimiter line")
}

func TestForms_ACutFormWithItsDelimiterInAPart(t *testing.T) {
	// The content of a part may hold "--b--": within a line it isn't a delimiter, and Go's reader of a CRLF body
	// skips it after a bare LF too. A body cut after it, inside the headers of a later part, is still cut. A body
	// shorter than its Content-Length is cut; without a length, a body is whole only when the close delimiter starts a
	// line. Either way it keeps no field.
	tests := []struct {
		name    string
		value   string
		chunked bool
	}{
		{"within a line", "x--b--y", false},
		{"within a line, chunked", "x--b--y", true},
		{"after a bare LF", "x\n--b--\ny", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			w := multipart.NewWriter(&buf)
			require.NoError(t, w.SetBoundary("b"))
			require.NoError(t, w.WriteField("a", tc.value))

			fw, err := w.CreateFormFile("f", "f.bin")
			require.NoError(t, err)

			_, err = fw.Write([]byte("file"))
			require.NoError(t, err)
			require.NoError(t, w.Close())

			body := buf.Bytes()
			cut := bytes.Index(body, []byte("filename")) // inside the headers of the file part

			// Go keeps the field before such a cut.
			r, err := stdlibForm(w.FormDataContentType(), body[:cut])
			require.NoError(t, err)
			require.Equal(t, tc.value, r.PostForm.Get("a"))

			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).MaxBodyParsingLimit(int64(cut)))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Postf("/form").FormField("a", match.StrictEqual(tc.value)).Reply(OK()))

			var reader io.Reader = bytes.NewReader(body)
			if tc.chunked {
				reader = io.MultiReader(reader) // the client can't know the length, so it sends the body chunked
			}

			req, err := http.NewRequest(http.MethodPost, m.URL("/form"), reader)
			require.NoError(t, err)
			req.Header.Set(HeaderContentType, w.FormDataContentType())

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, StatusNoMatch, res.StatusCode)
			require.Contains(t, logs.String(), "request_body_parser")
		})
	}
}

func TestForms_ABoundaryThatNamesAMediaType(t *testing.T) {
	// A boundary may hold a media type, quoted. The text and urlencoded parsers took such a form for theirs when they
	// read the Content-Type as text, and it had no fields and no files.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/upload").
		FormField("title", match.StrictEqual("Holiday")).
		FormFile("photo", FileWith().Filename(match.Equal("a.png"))).
		Reply(OK()))

	boundaries := []string{MIMEApplicationJSON, MIMETextPlain, MIMEFormURLEncoded}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			var body bytes.Buffer

			w := multipart.NewWriter(&body)
			require.NoError(t, w.SetBoundary(boundary))
			require.NoError(t, w.WriteField("title", "Holiday"))

			fw, err := w.CreateFormFile("photo", "a.png")
			require.NoError(t, err)

			_, err = fw.Write([]byte("PNG"))
			require.NoError(t, err)
			require.NoError(t, w.Close())

			res, _ := post(t, m.URL("/upload"), w.FormDataContentType(), body.Bytes())
			require.Equal(t, http.StatusOK, res.StatusCode, "Content-Type: %s", w.FormDataContentType())
		})
	}

	scoped.AssertNumberOfCalls(t, int64(len(boundaries)))
}

func TestForms_TemplatesWithoutAForm(t *testing.T) {
	// A request without a form reads as one without fields or files: templates don't fail on it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/no-form").Reply(OK().BodyTemplate(`[{{ .Request.PostForm.Get "a" }}][{{ with .Request.FormFile "f" }}{{ .Filename }}{{ end }}]`)))

	res, body := fetch(t, m.URL("/no-form"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "[][]", string(body))
}

func TestPropForms_TemplatesReadTheForm(t *testing.T) {
	// M24: a reply template reads the form as net/http parsed it: .Request.PostForm holds the text fields, and
	// .Request.FormFile the first file of a field, as http.Request.FormFile picks it.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		form := genForm().Draw(t, "form")
		contentType, body := form.encode(t)

		fields := make([]string, 0, len(form.fields)+1)
		files := make([]string, 0, len(form.files)+1)

		for _, f := range form.fields {
			fields = append(fields, f.name)
		}

		for _, f := range form.files {
			files = append(files, f.field)
		}

		// A name that isn't in the form reads as nothing.
		fields = append(fields, "absent")
		files = append(files, "absent")

		var tmpl strings.Builder
		for i, name := range fields {
			fmt.Fprintf(&tmpl, "field%d={{ .Request.PostForm.Get %q }}\n", i, name)
		}

		for i, name := range files {
			fmt.Fprintf(&tmpl, `file%d={{ with .Request.FormFile %q }}{{ .Filename }}|{{ .Size }}|{{ .Header.Get "Content-Type" }}{{ end }}`+"\n", i, name)
		}

		r, err := stdlibForm(contentType, body)
		require.NoError(t, err)

		var want strings.Builder
		for i, name := range fields {
			fmt.Fprintf(&want, "field%d=%s\n", i, r.PostForm.Get(name))
		}

		for i, name := range files {
			fmt.Fprintf(&want, "file%d=", i)

			if fh := r.MultipartForm.File[name]; len(fh) > 0 {
				fmt.Fprintf(&want, "%s|%d|%s", fh[0].Filename, fh[0].Size, fh[0].Header.Get(HeaderContentType))
			}

			want.WriteString("\n")
		}

		scope, err := m.Mock(Postf("/form").Reply(OK().BodyTemplate(tmpl.String())))
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		res, got := post(t, m.URL("/form"), contentType, body)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", got)
		require.Equal(t, want.String(), string(got))
	})
}

func TestPropForms_BrokenBodiesNeverMatchAPartialForm(t *testing.T) {
	// M25: a form body that is cut short or corrupted is matched on what net/http parses, all or nothing: a field
	// read before the damage never matches once the body fails to parse. The search goes on to the catch-all, a
	// warning says why, and nothing panics.
	t.Parallel()

	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		form := genForm().Draw(t, "form")
		field := formField{name: genFormName().Draw(t, "matched name"), value: rapid.StringN(1, 8, -1).Draw(t, "matched value")}
		at := rapid.IntRange(0, len(form.fields)).Draw(t, "matched field position")
		form.fields = slices.Insert(form.fields, at, field)

		contentType, body := form.encode(t)

		// The close delimiter ends the body; a cut before its end loses it.
		end := bytes.LastIndex(body, []byte("--")) + 2
		if rapid.Bool().Draw(t, "cut") {
			body = body[:rapid.IntRange(1, end-1).Draw(t, "cut at")]
		} else {
			body = slices.Clone(body)
			for range rapid.IntRange(1, 3).Draw(t, "corruptions") {
				body[rapid.IntRange(0, len(body)-1).Draw(t, "corrupt at")] = rapid.Byte().Draw(t, "corrupt with")
			}
		}

		scope, err := m.Mock(
			Postf("/form").FormField(field.name, match.StrictEqual(field.value)).Reply(OK().BodyText("form")),
			Postf("/form").Priority(1).Reply(Accepted().BodyText("catch-all")),
		)
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		// The oracle is net/http's parse, except that a body without a line that starts with its close delimiter
		// keeps no field: ReadForm keeps the parts before some cuts (TestForms_ACutFormKeepsNoField).
		r, parseErr := stdlibForm(contentType, body)
		closing := []byte("--" + boundaryOf(t, contentType) + "--")
		if !slices.ContainsFunc(bytes.Split(body, []byte("\n")), func(line []byte) bool { return bytes.HasPrefix(line, closing) }) {
			parseErr = errFormCut
		}

		want := "catch-all"
		if parseErr == nil && r.PostForm.Get(field.name) == field.value {
			want = "form"
		}

		warnings := strings.Count(logs.String(), "request_body_parser")

		_, got := post(t, m.URL("/form"), contentType, body)
		require.Equal(t, want, string(got))

		if parseErr != nil {
			require.Greater(t, strings.Count(logs.String(), "request_body_parser"), warnings, "a warning says why")
		}

		require.NotContains(t, logs.String(), "panic during request matching")
	})
}

// boundaryOf returns the boundary of a multipart content type.
func boundaryOf(t require.TestingT, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)

	return params["boundary"]
}

func FuzzFormRequest(f *testing.F) {
	// M27: no content type or body sent to a server holding form mocks panics it or stops the search: the answer is
	// the form mock or the catch-all.
	valid, body := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.png", contentType: "image/png", body: []byte("\x89PNG")}},
	}.encode(f)

	for _, seed := range []struct {
		contentType string
		body        []byte
	}{
		{valid, body},
		{valid, body[:len(body)/2]},
		{valid, body[:len(body)-6]},
		{"multipart/form-data", body},
		{"multipart/form-data; boundary=", body},
		{"multipart/mixed; boundary=x", []byte("--x\r\n\r\nx\r\n--x--\r\n")},
		{MIMEFormURLEncoded, []byte("a=1&a=%zz")},
		{"", nil},
	} {
		f.Add(seed.contentType, seed.body)
	}

	logs := &logBuffer{}
	logger := zerolog.New(logs).Level(zerolog.ErrorLevel)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	f.Cleanup(m.Close)

	m.MustMock(Postf("/form").FormField("a", match.Equal("1")).FormFile("f", FileWith()).Reply(OK().BodyText("form")))
	m.MustMock(Postf("/form").Priority(1).Reply(Accepted().BodyText("catch-all")))

	f.Fuzz(func(t *testing.T, contentType string, body []byte) {
		// A header value can't hold control characters: the client refuses to send it.
		if strings.ContainsFunc(contentType, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return
		}

		_, got := post(t, m.URL("/form"), contentType, body)
		require.Contains(t, []string{"form", "catch-all"}, string(got))
		require.NotContains(t, logs.String(), "panic during request matching")
	})
}

func BenchmarkMultipartForm(b *testing.B) {
	// A 1 MB upload through the parser chain: net/http parses it in memory, a copy of the body at most.
	files := make([]formFile, 50)
	for i := range files {
		files[i] = formFile{field: "f", filename: "f.bin", body: bytes.Repeat([]byte{byte(i)}, 20<<10)}
	}

	contentType, body := formModel{fields: []formField{{name: "a", value: "1"}}, files: files}.encode(b)
	app := MustNew(Setup().Silent())

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()

	for b.Loop() {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		r.Header.Set(HeaderContentType, contentType)

		if _, _, _, err := parseRequestBody(app, r); err != nil {
			b.Fatal(err)
		}
	}
}

// Generators

// formModel is a multipart form, in the order its parts are sent: text fields, then files.
type formModel struct {
	fields []formField
	files  []formFile
}

type formField struct {
	name  string
	value string
}

type formFile struct {
	field       string
	filename    string
	contentType string            // "" sends no Content-Type
	header      map[string]string // more headers of the part
	body        []byte
}

// _genFileTypes are the Content-Type values files are sent with, "" for none.
var _genFileTypes = []string{"", "image/png", "text/plain; charset=utf-8", "Application/JSON", "application/octet-stream"}

// genFormName draws a field name with the characters a Content-Disposition quotes or escapes.
// CR and LF are left out: Go writes them as %0D and %0A, so such a name doesn't arrive as written.
// TestForms_LineBreaksInNamesArriveEscaped pins it.
func genFormName() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-\[\]"\\;=éü]{1,8}`)
}

// genFilename draws a file name. "/" is left out: Go keeps the base of a file name, so a/b arrives as b.
// TestFormFile_FilenamesArriveAsTheirBase pins it.
func genFilename() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-"\\;=éü]{1,10}`)
}

// genFileBody draws a file's content: empty, text, or bytes with line breaks and dashes, as a boundary has.
func genFileBody() *rapid.Generator[[]byte] {
	return rapid.OneOf(
		rapid.Just([]byte{}),
		rapid.Map(rapid.String(), func(s string) []byte { return []byte(s) }),
		rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"\r\n", "--", "\r\n--", "\x00", "\xff", "a"}), 1, 12),
			func(chunks []string) []byte {
				var b bytes.Buffer
				for _, c := range chunks {
					b.WriteString(c)
				}

				return b.Bytes()
			}),
	)
}

func genForm() *rapid.Generator[formModel] {
	return rapid.Custom(func(t *rapid.T) formModel {
		var f formModel
		for range rapid.IntRange(0, 3).Draw(t, "fields") {
			f.fields = append(f.fields, formField{
				name:  genFormName().Draw(t, "field name"),
				value: rapid.String().Draw(t, "field value"),
			})
		}

		for range rapid.IntRange(0, 3).Draw(t, "files") {
			f.files = append(f.files, formFile{
				field:       genFormName().Draw(t, "file field"),
				filename:    genFilename().Draw(t, "filename"),
				contentType: rapid.SampledFrom(_genFileTypes).Draw(t, "file content type"),
				body:        genFileBody().Draw(t, "file body"),
			})
		}

		return f
	})
}

// encode writes the form with Go's multipart writer, under a fresh random boundary.
func (f formModel) encode(t require.TestingT) (contentType string, body []byte) {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	for _, field := range f.fields {
		require.NoError(t, w.WriteField(field.name, field.value))
	}

	for _, file := range f.files {
		h := make(textproto.MIMEHeader)
		h.Set(HeaderContentDisposition, multipart.FileContentDisposition(file.field, file.filename))

		if file.contentType != "" {
			h.Set(HeaderContentType, file.contentType)
		}

		for k, v := range file.header {
			h.Set(k, v)
		}

		part, err := w.CreatePart(h)
		require.NoError(t, err)

		_, err = part.Write(file.body)
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())

	return w.FormDataContentType(), buf.Bytes()
}

// stdlibForm parses a body as net/http does: the oracle of what a server sees.
func stdlibForm(contentType string, body []byte) (*http.Request, error) {
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set(HeaderContentType, contentType)

	return r, r.ParseMultipartForm(int64(len(body)))
}

// goneFiles parses a form to temporary files and removes them, as net/http does once the request is done: its files
// can't be opened any more.
func goneFiles(t require.TestingT, form formModel) *http.Request {
	contentType, body := form.encode(t)

	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set(HeaderContentType, contentType)
	require.NoError(t, r.ParseMultipartForm(0)) // no memory for files: each goes to a temporary file
	require.NoError(t, r.MultipartForm.RemoveAll())

	return r
}

// post sends a body and reads the whole response.
func post(t require.TestingT, url, contentType string, body []byte) (*http.Response, []byte) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set(HeaderContentType, contentType)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := readAll(res)
	require.NoError(t, err)

	return res, b
}

func readAll(res *http.Response) ([]byte, error) {
	defer res.Body.Close()

	var b bytes.Buffer
	_, err := b.ReadFrom(res.Body)

	return b.Bytes(), err
}
