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
