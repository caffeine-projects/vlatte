package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestOpenAPI(t *testing.T) {
	// The document is served in both formats, which say the same.
	url := serve(t, newStore(t))

	res, asJSON := send(t, request{method: http.MethodGet, url: url + "/openapi"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))

	res, asYAML := send(t, request{method: http.MethodGet, url: url + "/openapi", accept: "application/yaml"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/yaml", res.Header.Get("Content-Type"))

	fromJSON, err := config.Parse([]byte(asJSON), "json")
	require.NoError(t, err)

	fromYAML, err := config.Parse([]byte(asYAML), "yaml")
	require.NoError(t, err)

	require.Equal(t, fromYAML, fromJSON)
	require.Equal(t, "3.2.0", fromJSON.(map[string]any)["openapi"], "3.2 documents the QUERY method of /requests")
}

func TestDocs(t *testing.T) {
	// The documentation page renders the admin API's own document, with a renderer pinned to a version and its hash,
	// so a changed file on the CDN doesn't run.
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: url + "/docs"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
	require.Contains(t, body, `url: 'openapi'`)
	require.Regexp(t, `src="https://cdn\.jsdelivr\.net/npm/@scalar/api-reference@\d+\.\d+\.\d+/dist/browser/standalone\.js"`, body)
	require.Regexp(t, `integrity="sha384-[A-Za-z0-9+/=]+"`, body)
}
