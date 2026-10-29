package handler_test

import (
	"html"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/buildinfo"
)

func TestHome_ServesTheHomepageAtTheRootInPlaceOfTheDocs(t *testing.T) {
	// The root of the admin API is its homepage: the docs live at /docs only, which the homepage links to.
	base := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: base + "/"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
	require.Contains(t, body, "<title>vlatte admin</title>")
	require.NotContains(t, body, "@scalar/api-reference")
	require.Contains(t, body, `<a href="docs">API docs</a>`)
}

func TestHome_ShowsTheVersionOfVlatte(t *testing.T) {
	// The server box tells which vlatte serves, which no endpoint of the admin API answers: the server fills it in,
	// with the version vlatte version prints for the same build, devel included.
	base := serve(t, newStore(t))

	_, body := send(t, request{method: http.MethodGet, url: base + "/"})
	require.NotContains(t, body, "{vlatte-version}")
	require.Contains(t, body, "<dt>Version</dt><dd><code>"+html.EscapeString(buildinfo.Version())+"</code></dd>")
}

func TestHome_ReadsTheAdminAPIByRelativeURLs(t *testing.T) {
	// The boxes read the endpoints of the admin API next to the page, so it works wherever the admin API is mounted,
	// and the page follows the server's events to stay current.
	base := serve(t, newStore(t))

	_, body := send(t, request{method: http.MethodGet, url: base + "/"})
	for _, read := range []string{
		"call('GET', 'health')",
		"call('GET', 'readyz')",
		"call('GET', 'mocks')",
		"call('GET', 'requests?limit=0')",
		"call('QUERY', 'requests', { outcome, limit: 0 })",
		"call('GET', 'scenarios')",
		"call('GET', 'recording')",
		"new EventSource('events')",
	} {
		require.Contains(t, body, read)
	}
}
