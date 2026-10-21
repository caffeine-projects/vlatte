package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestSimilarRequestURLPaths(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	scope := m.MustMock(
		mockhttp.Getf("/customers").Reply(mockhttp.OK()),
		mockhttp.Getf("/customers/100").Reply(mockhttp.Accepted()),
		mockhttp.Getf("/customers/100/orders").Reply(mockhttp.Unauthorized()),
		mockhttp.Getf("/customers/100/orders/BR-500").Reply(mockhttp.BadRequest()),
		mockhttp.Getf("/customers/100/orders/BR-500/items").Reply(mockhttp.InternalServerError()))

	res, err := http.DefaultClient.Get(m.URL() + "/customers")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders/BR-500")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders/BR-500/items")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/orders")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

	vlatte.Times(t, scope, 5)
}

func TestRequestURL_AnEscapedQuestionMarkIsPartOfThePath(t *testing.T) {
	// "%3F" is a "?" within the path: the request has no query, and the mock for /test doesn't match it.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.Getf("/test").Reply(mockhttp.OK().BodyTemplate("query={{ .Request.URL.RawQuery }}")),
		mockhttp.Getf("/test?q=none").Reply(mockhttp.OK().
			BodyTemplate("path={{ .Request.URL.Path }} query={{ .Request.URL.RawQuery }}")))

	res, body := getTarget(t, m, "/test%3Fq=none")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "path=/test?q=none query=", string(body))

	res, body = getTarget(t, m, "/test?q=none")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "query=q=none", string(body))
}

func TestRequestURL_AnEscapedSlashStaysInItsSegment(t *testing.T) {
	// "%2F" is a "/" within a segment: /files/a%2Fb has two segments, files and a/b.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/files/*").Reply(mockhttp.OK().
		BodyTemplate("{{ len .Request.URLPathSegments }} {{ index .Request.URLPathSegments 1 }}")))

	res, body := getTarget(t, m, "/files/a%2Fb")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "2 a/b", string(body))
}
