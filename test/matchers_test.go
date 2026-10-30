package test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestCustomMatchers_AMismatchLetsTheNextMockAnswer(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Getf("/test").Header("X-Code", match.Function(func(any) (bool, error) {
		return false, nil
	})).Reply(mockhttp.OK().BodyText("first")))
	m.MustMock(mockhttp.Getf("/test").Reply(mockhttp.OK().BodyText("fallback")))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/test"), nil)
	require.NoError(t, err)
	res, body := do(t, req)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "fallback", string(body))
}

func TestCustomMatchers_AnErrorIsAFailureInsteadOfAFallback(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Getf("/test").Header("X-Code", match.Function(func(any) (bool, error) {
		return false, errors.New("test: matcher unavailable")
	})).Reply(mockhttp.OK().BodyText("first")))
	m.MustMock(mockhttp.Getf("/test").Reply(mockhttp.OK().BodyText("fallback")))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/test"), nil)
	require.NoError(t, err)
	res, body := do(t, req)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, vlatte.FailureMatch, res.Header.Get(mockhttp.HeaderVlatteError))
	require.Contains(t, string(body), "test: matcher unavailable")
}
