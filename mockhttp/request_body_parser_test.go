package mockhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
