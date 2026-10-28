package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestAdminAuth_AsksEveryRequestForCredentials(t *testing.T) {
	// The admin API changes the mocks of a running server: with credentials set, a request without them changes
	// nothing, and one with them does.
	api := admin.MustNew(admin.Addr("127.0.0.1:0"), admin.BasicAuth("ops", "pw"), admin.BearerToken("tok"))
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(api))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Get(match.URLPath("/hello")).Reply(mockhttp.OK()))

	removeAll := func(set func(r *http.Request)) int {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, m.AdminURL()+"/mocks", nil)
		require.NoError(t, err)
		set(r)

		res, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	require.Equal(t, http.StatusUnauthorized, removeAll(func(*http.Request) {}))
	require.Equal(t, http.StatusUnauthorized, removeAll(func(r *http.Request) { r.SetBasicAuth("ops", "tok!") }))
	statusOf(t, m.URL("/hello"), http.StatusOK, "a request without the credentials removes nothing")

	require.Equal(t, http.StatusNoContent, removeAll(func(r *http.Request) { r.SetBasicAuth("ops", "pw") }))
	statusOf(t, m.URL("/hello"), mockhttp.StatusNoMatch, "a request with them removes the mocks")

	m.MustMock(mockhttp.Get(match.URLPath("/hello")).Reply(mockhttp.OK()))
	require.Equal(t, http.StatusNoContent, removeAll(func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }))
	statusOf(t, m.URL("/hello"), mockhttp.StatusNoMatch, "so does one with the token")

	statusOf(t, m.AdminURL()+"/readyz", http.StatusOK, "the probes need no credentials")
}

// statusOf requires a GET of url to answer status.
func statusOf(t *testing.T, url string, status int, msg string) {
	t.Helper()

	res, err := http.Get(url)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, status, res.StatusCode, msg)
}
