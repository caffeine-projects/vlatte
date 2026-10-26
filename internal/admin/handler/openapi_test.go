package handler_test

import (
	json "encoding/json/v2"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
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

func TestOpenAPI_DocumentsTheEvents(t *testing.T) {
	// GET /events streams the events of the mock server: the document says what a client of it reads.
	data, err := json.Marshal(adminapi.Event{
		Type: adminapi.EventMatched, Time: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), RequestID: "1", Method: "GET",
		Target: "/hello", MockID: "hello", Status: 200, Message: "matched",
	})
	require.NoError(t, err)

	conformsAs(t, string(data), "json", "Event")
}
