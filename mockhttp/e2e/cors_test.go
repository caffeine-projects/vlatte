package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestCORS(t *testing.T) {
	// A browser sends a cross-origin PUT with a custom header only after a preflight allows it, and lets scripts read
	// only the headers exposed to them. Header names are literals: browsers look for the names of the Fetch standard.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().CORS(&mockhttp.CORSConfig{
		AllowedOrigin:     "*",
		ExposeHeaders:     "X-Id",
		AllowedMethods:    "GET,PUT",
		SuccessStatusCode: http.StatusNoContent,
	}))
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.AnyMethod().Reply(mockhttp.OK().Header("X-Id", "42")))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, m.URL("/a"), nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.org")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	req.Header.Set("Access-Control-Request-Headers", "X-Custom")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
	require.Equal(t, []string{"GET,PUT"}, res.Header["Access-Control-Allow-Methods"])
	require.Equal(t, []string{"X-Custom"}, res.Header["Access-Control-Allow-Headers"])

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPut, m.URL("/a"), nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.org")
	req.Header.Set("X-Custom", "1")

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"*"}, res.Header["Access-Control-Allow-Origin"])
	require.Equal(t, []string{"X-Id"}, res.Header["Access-Control-Expose-Headers"])
	require.Equal(t, []string{"42"}, res.Header["X-Id"])
}
