package xhttp

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp/httpval"
)

func TestRequestBodyParsers_CanParse(t *testing.T) {
	formParser := &formURLEncodedParser{}
	textParser := &plainTextParser{}
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
		{"Form: can parse application/form-url-encoded", httpval.MIMEFormURLEncoded, formParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMEFormURLEncoded}), true},
		{"Form: can parse application/form-url-encoded; charset=UTF-8", httpval.MIMEFormURLEncodedCharsetUTF8, formParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMEFormURLEncodedCharsetUTF8}), true},
		{"Form: should not parse", httpval.MIMEApplicationJSON, formParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMEApplicationJSON}), false},

		{"Text: can parse text/plain", httpval.MIMETextPlain, textParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMETextPlain}), true},
		{"Text: can parse text/plain; charset=UTF-8", httpval.MIMETextPlainCharsetUTF8, textParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMETextPlainCharsetUTF8}), true},
		{"Text: should parse JSON as text", httpval.MIMEApplicationJSON, textParser, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMEApplicationJSON}), true},

		{"Noop: should always parse", httpval.MIMETextPlain, noop, newReq(map[string]string{httpval.HeaderContentType: httpval.MIMETextPlain}), true},
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
			req.Header.Add(httpval.HeaderContentType, httpval.MIMEFormURLEncoded)
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
	m.MustMock(Postf("/test").Body(Eq("hello")).Reply(Func(func(w http.ResponseWriter, rv *RequestValues) (*Response, error) {
		require.Equal(t, "hello", rv.ParsedBodyData.(string))
		return &Response{StatusCode: http.StatusOK, Body: []byte(rv.ParsedBodyData.(string))}, nil
	})))
	m.MustStart()

	defer m.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodPost, m.URL("/test"), strings.NewReader("hello world"))
	req.Header.Add(httpval.HeaderContentType, httpval.MIMETextPlainCharsetUTF8)

	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestParseURL_SegmentsExcludeQuery(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "http://localhost/a/b?x=1", nil)
	require.NoError(t, err)

	u, segments := parseURL(r, "http://localhost")

	// Path segments come from the path only; the query stays in the full URL.
	require.Equal(t, []string{"a", "b"}, segments)
	require.Equal(t, "x=1", u.RawQuery)
}
