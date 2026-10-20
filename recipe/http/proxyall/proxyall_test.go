package proxyall_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/recipe/http/proxyall"
)

func TestProxyAll(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "backend "+r.URL.Path)
	}))
	t.Cleanup(backend.Close)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	require.NoError(t, m.Prep(t.Context(), proxyall.New(proxyall.Config{Target: backend.URL})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/mocked").Reply(mockhttp.OK().PlainText("mock")))

	get := func(path string) string {
		res, err := http.Get(m.URL(path))
		require.NoError(t, err)
		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		return string(b)
	}

	// The recipe forwards whatever no other mock matches, so the server stays a mock first.
	require.Equal(t, "mock", get("/mocked"))
	require.Equal(t, "backend /anything/else", get("/anything/else"))
}

func TestProxyAll_VerifiesTheBackendWhenAsked(t *testing.T) {
	// SSLVerify has the recipe's reply verify its backend's certificate: one the server doesn't trust fails the call.
	// Without it, the reply's default, the call goes through.
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "backend")
	}))
	t.Cleanup(backend.Close)

	for _, verify := range []bool{true, false} {
		m := mockhttp.MustNew(mockhttp.Setup().Silent())
		require.NoError(t, m.Prep(t.Context(), proxyall.New(proxyall.Config{Target: backend.URL, SSLVerify: verify})))
		m.MustStart()
		t.Cleanup(m.Close)

		res, err := http.Get(m.URL("/x"))
		require.NoError(t, err)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		if verify {
			require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
			require.Contains(t, string(body), "certificate")
		} else {
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "backend", string(body))
		}
	}
}
