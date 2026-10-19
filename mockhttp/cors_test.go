package mockhttp

import (
	"net/http"
	"slices"
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
		require.Subset(t, varies(res), []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"})
		require.NotContains(t, varies(res), "Access-Control-Request-Header")
		require.Empty(t, res.Header["X-Mock"], "a preflight is the server's to answer")
	})

	t.Run("allowed headers are sent as set, not as asked for", func(t *testing.T) {
		// A browser checks the headers it asks for against the allowed ones: a header that isn't allowed fails.
		res := preflight(t, corsServer(t, CORS().AllowedHeaders("X-A", "X-B")), "https://example.org")

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

	t.Run("* allows the method and the headers a preflight asks for", func(t *testing.T) {
		// Browsers read * as a name for requests with credentials, and never as Authorization.
		m := corsServer(t, CORS().AllowCredentials(true).AllowMethods("*").AllowedHeaders("*"))

		res, _ := do(t, http.MethodOptions, m.URL("/a"), http.Header{
			"Origin":                         {"https://example.org"},
			"Access-Control-Request-Method":  {http.MethodPatch},
			"Access-Control-Request-Headers": {"authorization,x-custom"},
		}, nil)

		require.Equal(t, []string{http.MethodPatch}, res.Header["Access-Control-Allow-Methods"])
		require.Equal(t, []string{"authorization,x-custom"}, res.Header["Access-Control-Allow-Headers"])
	})

	t.Run("max age says how long browsers may cache the preflight", func(t *testing.T) {
		res := preflight(t, corsServer(t, CORS().MaxAge(600)), "https://example.org")
		require.Equal(t, []string{"600"}, res.Header["Access-Control-Max-Age"])

		res = preflight(t, corsServer(t, CORS().MaxAge(-1)), "https://example.org")
		require.NotContains(t, res.Header, "Access-Control-Max-Age")
	})

	t.Run("answers with a custom status code", func(t *testing.T) {
		// A preflight answered with a status other than 2xx fails in browsers: a way to test that failure.
		res := preflight(t, corsServer(t, CORS().SuccessStatusCode(http.StatusBadRequest)), "https://example.org")

		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
	})

	t.Run("a config without a status code answers 204", func(t *testing.T) {
		res := preflight(t, corsServer(t, &CORSConfig{AllowedOrigin: "*"}), "https://example.org")

		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.NotContains(t, res.Header, "X-Vlatte-Error")
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

	t.Run("an OPTIONS request that isn't a preflight goes to the mocks", func(t *testing.T) {
		// A preflight has an origin and the method it asks for. An API answers other OPTIONS requests itself.
		tests := []struct {
			name   string
			header http.Header
		}{
			{"without Access-Control-Request-Method", http.Header{"Origin": {"https://example.org"}}},
			{"without Origin", http.Header{"Access-Control-Request-Method": {http.MethodPut}}},
		}

		m := corsServer(t)

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res, _ := do(t, http.MethodOptions, m.URL("/a"), tt.header, nil)

				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, []string{"1"}, res.Header["X-Mock"])
				require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
				require.NotContains(t, res.Header, "Access-Control-Allow-Methods")
			})
		}
	})
}

func TestCORS_Origin(t *testing.T) {
	// Browsers take one allowed origin, or *. The answer depends on the origin, so caches must keep the answers of
	// different origins apart: it varies by Origin, even when the origin isn't allowed.
	tests := []struct {
		name    string
		allowed string
		origin  string
		want    []string
		varies  bool
	}{
		{"any", "*", "https://a.org", []string{"*"}, true},
		{"the one allowed", "https://a.org", "https://a.org", []string{"https://a.org"}, true},
		{"one of a list with blanks", "https://a.org, https://b.org", "https://b.org", []string{"https://b.org"}, true},
		{"one not in the list", "https://a.org, https://b.org", "https://evil.org", nil, true},
		{"one not allowed", "https://a.org", "https://evil.org", nil, true},
		{"no origin", "https://a.org,https://b.org", "", nil, true},
		{"none allowed", "", "https://a.org", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := corsServer(t, &CORSConfig{AllowedOrigin: tt.allowed})

			header := http.Header{}
			if tt.origin != "" {
				header.Set("Origin", tt.origin)
			}

			res, _ := do(t, http.MethodGet, m.URL("/a"), header, nil)
			answers := []*http.Response{res}

			if tt.origin != "" {
				answers = append(answers, preflight(t, m, tt.origin))
			}

			for _, res := range answers {
				require.Equal(t, tt.want, res.Header["Access-Control-Allow-Origin"], res.Request.Method)
				require.Equal(t, tt.varies, slices.Contains(varies(res), "Origin"), res.Request.Method)
			}
		})
	}

	t.Run("any origin with credentials is the request's", func(t *testing.T) {
		// Browsers refuse * for requests with credentials, such as cookies.
		m := corsServer(t, CORS().AllowCredentials(true))

		res, _ := do(t, http.MethodGet, m.URL("/a"), http.Header{"Origin": {"https://a.org"}}, nil)

		for _, res := range []*http.Response{res, preflight(t, m, "https://a.org")} {
			require.Equal(t, []string{"https://a.org"}, res.Header["Access-Control-Allow-Origin"], res.Request.Method)
			require.Equal(t, []string{"true"}, res.Header["Access-Control-Allow-Credentials"], res.Request.Method)
			require.Contains(t, varies(res), "Origin", res.Request.Method)
		}
	})
}

func TestCORS_Builder(t *testing.T) {
	// The builder sets only what it is told to: the defaults, and what other configurers set, stay.
	t.Run("keeps the default methods and status", func(t *testing.T) {
		res := preflight(t, corsServer(t, CORS().AllowOrigin("https://example.org")), "https://example.org")

		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.Equal(t, []string{DefaultCORSConfig.AllowedMethods}, res.Header["Access-Control-Allow-Methods"])
	})

	t.Run("keeps the default origin", func(t *testing.T) {
		res := preflight(t, corsServer(t, CORS().AllowCredentials(true)), "https://example.org")

		require.NotEmpty(t, res.Header["Access-Control-Allow-Origin"])
		require.Equal(t, []string{"true"}, res.Header["Access-Control-Allow-Credentials"])
	})

	t.Run("keeps what other configurers set", func(t *testing.T) {
		res := preflight(t, corsServer(t, CORS().AllowCredentials(true), CORS().MaxAge(60)), "https://example.org")

		require.Equal(t, []string{"true"}, res.Header["Access-Control-Allow-Credentials"])
		require.Equal(t, []string{"60"}, res.Header["Access-Control-Max-Age"])
	})
}
