package githubapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApp_RefusesACallbackWithoutTheVerifier(t *testing.T) {
	// The state alone doesn't prove the browser that started the sign-in is the one that ends it: without the code
	// verifier the exchange can't prove it either, so the app doesn't try it.
	app := New(Config{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/callback?state=s&code=c", nil)
	req.AddCookie(&http.Cookie{Name: StateCookie, Value: "s"})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "githubapp: invalid state\n", rec.Body.String())
}
