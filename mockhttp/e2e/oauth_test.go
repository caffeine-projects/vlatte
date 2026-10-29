package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// Helpers of the tests that sign in through an OAuth or OpenID Connect provider mocked with vlatte. The app under
// test serves /login, which sends the browser to the provider, and /callback, where the provider sends it back.

// newBrowser returns a client that keeps cookies and follows redirects, as a browser does, and trusts app.
func newBrowser(t *testing.T, app *httptest.Server) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	browser := &http.Client{Jar: jar, Transport: app.Client().Transport}
	t.Cleanup(browser.CloseIdleConnections)

	return browser
}

// noRedirects returns a client that shares the cookies of browser but stops at the first redirect.
func noRedirects(browser *http.Client) *http.Client {
	return &http.Client{
		Jar:           browser.Jar,
		Transport:     browser.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startSignIn sends browser to /login, stops it at the redirect to the provider, and returns the state the app gave
// it.
func startSignIn(t *testing.T, browser *http.Client, app *httptest.Server) string {
	t.Helper()

	return redirect(t, browser, app.URL+"/login").Query().Get("state")
}

// consent sends browser to /login and on to the provider, stops it at the redirect back to the app, and returns where
// the provider sends it: the app's callback with the code and the state. The callback is not called yet.
func consent(t *testing.T, browser *http.Client, app *httptest.Server) *url.URL {
	t.Helper()

	return redirect(t, browser, redirect(t, browser, app.URL+"/login").String())
}

// redirect GETs u with the cookies of browser and returns where it redirects to.
func redirect(t *testing.T, browser *http.Client, u string) *url.URL {
	t.Helper()

	res, err := noRedirects(browser).Get(u)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusFound, res.StatusCode)

	location, err := res.Location()
	require.NoError(t, err)

	return location
}

func browse(t *testing.T, browser *http.Client, u string) (int, string) {
	t.Helper()

	res, err := browser.Get(u)
	require.NoError(t, err)
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(b)
}
