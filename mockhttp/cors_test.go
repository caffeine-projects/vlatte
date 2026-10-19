package mockhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The tests name headers with literals, not the Header constants: a misspelled constant would agree with itself, and
// browsers look for the names of the Fetch standard.

// corsServer starts a server with CORS, whose mock answers every request that reaches it with 200 and X-Mock: 1.
func corsServer(t *testing.T, options ...CORSConfigurer) *Server {
	t.Helper()

	m := MustNew(Setup().Silent().CORS(options...))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(OK().Header("X-Mock", "1")))

	return m
}

// preflight sends what a browser at origin sends before a cross-origin PUT with an X-Custom header.
func preflight(t *testing.T, m *Server, origin string) *http.Response {
	t.Helper()

	res, _ := do(t, http.MethodOptions, m.URL("/a"), http.Header{
		"Origin":                         {origin},
		"Access-Control-Request-Method":  {http.MethodPut},
		"Access-Control-Request-Headers": {"X-Custom"},
	}, nil)

	return res
}

// varies lists the request headers the Vary headers of res name.
func varies(res *http.Response) []string {
	var names []string

	for _, v := range res.Header.Values("Vary") {
		for name := range strings.SplitSeq(v, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}

	return names
}

func TestCORS_Preflight(t *testing.T) {
	t.Run("allows the headers a browser asks for, under their standard names", func(t *testing.T) {
		// A browser sends a request with custom headers only when the preflight allows each one of them.
		res := preflight(t, corsServer(t), "https://example.org")

		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
		require.Equal(t, []string{DefaultCORSConfig.AllowedMethods}, res.Header["Access-Control-Allow-Methods"])
		require.Equal(t, []string{"X-Custom"}, res.Header["Access-Control-Allow-Headers"])
		require.NotContains(t, res.Header, "Access-Control-Allow-Header")
		require.Contains(t, varies(res), "Access-Control-Request-Headers")
		require.NotContains(t, varies(res), "Access-Control-Request-Header")
		require.Empty(t, res.Header["X-Mock"], "a preflight is the server's to answer")
	})

	t.Run("allowed headers are sent as set, not as asked for", func(t *testing.T) {
		// A browser checks the headers it asks for against the allowed ones: a header that isn't allowed fails.
		res := preflight(t, corsServer(t, CORS().AllowOrigin("*").AllowedHeaders("X-A", "X-B")), "https://example.org")

		require.Equal(t, []string{"X-A,X-B"}, res.Header["Access-Control-Allow-Headers"])
	})

	t.Run("a preflight that asks for no headers gets no allowed headers", func(t *testing.T) {
		m := corsServer(t)

		res, _ := do(t, http.MethodOptions, m.URL("/a"), http.Header{
			"Origin":                        {"https://example.org"},
			"Access-Control-Request-Method": {http.MethodPut},
		}, nil)

		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.NotContains(t, res.Header, "Access-Control-Allow-Headers")
	})

	t.Run("max age says how long browsers may cache the preflight", func(t *testing.T) {
		res := preflight(t, corsServer(t, CORS().AllowOrigin("*").MaxAge(600)), "https://example.org")
		require.Equal(t, []string{"600"}, res.Header["Access-Control-Max-Age"])

		res = preflight(t, corsServer(t, CORS().AllowOrigin("*").MaxAge(-1)), "https://example.org")
		require.NotContains(t, res.Header, "Access-Control-Max-Age")
	})

	t.Run("answers with a custom status code", func(t *testing.T) {
		// A preflight answered with a status other than 2xx fails in browsers: a way to test that failure.
		res := preflight(t, corsServer(t, CORS().AllowOrigin("*").SuccessStatusCode(http.StatusBadRequest)), "https://example.org")

		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
	})
}

func TestCORS_Request(t *testing.T) {
	t.Run("exposes headers under the standard name, next to the mock's answer", func(t *testing.T) {
		// Browser scripts read only the response headers that are exposed to them.
		m := corsServer(t, &CORSConfig{AllowedOrigin: "*", ExposeHeaders: "X-Id,X-Mock"})

		res, _ := do(t, http.MethodGet, m.URL("/a"), http.Header{"Origin": {"https://example.org"}}, nil)

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, []string{"1"}, res.Header["X-Mock"])
		require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
		require.Equal(t, []string{"X-Id,X-Mock"}, res.Header["Access-Control-Expose-Headers"])
		require.NotContains(t, res.Header, "Access-Control-Expose-Header")
	})

	t.Run("an empty allowed origin allows none", func(t *testing.T) {
		m := corsServer(t, CORS().AllowOrigin(""))

		res, _ := do(t, http.MethodGet, m.URL("/a"), http.Header{"Origin": {"https://example.org"}}, nil)

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.NotContains(t, res.Header, "Access-Control-Allow-Origin")
	})
}
