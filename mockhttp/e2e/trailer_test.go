package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestTrailer_WithBody(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		mockhttp.Get(match.URLPath("/test")).
			Reply(mockhttp.OK().
				PlainText("hello world").
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Trailer("trailer-1", "trailer-1-value").
				Trailer("trailer-2", "trailer-2-value")))

	res, err := http.Get(m.URL() + "/test")
	require.NoError(t, err)

	defer res.Body.Close()

	require.True(t, scoped.AssertCalled(t))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, mockhttp.MIMETextPlain, res.Header.Get(mockhttp.HeaderContentType))
	require.Len(t, res.Trailer, 2)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.Len(t, res.Trailer, 2)
	require.Equal(t, "hello world", string(b))
	require.Equal(t, "trailer-1-value", res.Trailer.Get("trailer-1"))
	require.Equal(t, "trailer-2-value", res.Trailer.Get("trailer-2"))
}

func TestTrailer_WithoutBody(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		mockhttp.Get(match.URLPath("/test")).
			Reply(mockhttp.OK().
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Trailer("trailer-1", "trailer-1-value").
				Trailer("trailer-2", "trailer-2-value")))

	res, err := http.Get(m.URL() + "/test")

	require.NoError(t, err)
	require.True(t, scoped.AssertCalled(t))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, mockhttp.MIMETextPlain, res.Header.Get(mockhttp.HeaderContentType))
	require.Len(t, res.Trailer, 2)

	_, err = io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Len(t, res.Trailer, 2)
	require.Equal(t, "trailer-1-value", res.Trailer.Get("trailer-1"))
	require.Equal(t, "trailer-2-value", res.Trailer.Get("trailer-2"))
}
