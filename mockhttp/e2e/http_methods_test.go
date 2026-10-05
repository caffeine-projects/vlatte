package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestHTTPMethods(t *testing.T) {
	client := &http.Client{}

	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	const path1 = "/test/1"
	const path2 = "/test/2"
	const path3 = "/test/3"

	testCases := []struct {
		method string
		path   string
		mock   *mockhttp.Builder
		status int
	}{
		{http.MethodGet, path1, mockhttp.Getf(path1), 200},
		{http.MethodGet, path2, mockhttp.Get(match.URLPathf(path2)), 400},
		{http.MethodGet, path3, mockhttp.Request().Method(http.MethodGet).URLPathf(path3), 500},

		{http.MethodPost, path1, mockhttp.Postf(path1), 201},
		{http.MethodPost, path2, mockhttp.Post(match.URLPathf(path2)), 401},
		{http.MethodPost, path3, mockhttp.Request().Method(http.MethodPost).URLPathf(path3), 501},

		{http.MethodPut, path1, mockhttp.Putf(path1), 202},
		{http.MethodPut, path2, mockhttp.Put(match.URLPathf(path2)), 402},
		{http.MethodPut, path3, mockhttp.Request().Method(http.MethodPut).URLPathf(path3), 502},

		{http.MethodPatch, path1, mockhttp.Patchf(path1), 203},
		{http.MethodPatch, path2, mockhttp.Patch(match.URLPathf(path2)), 403},
		{http.MethodPatch, path3, mockhttp.Request().Method(http.MethodPatch).URLPathf(path3), 503},

		{http.MethodDelete, path1, mockhttp.Deletef(path1), 204},
		{http.MethodDelete, path2, mockhttp.Delete(match.URLPathf(path2)), 404},
		{http.MethodDelete, path3, mockhttp.Request().Method(http.MethodDelete).URLPathf(path3), 504},

		{http.MethodHead, path1, mockhttp.Headf(path1), 205},
		{http.MethodHead, path2, mockhttp.Head(match.URLPathf(path2)), 405},
		{http.MethodHead, path3, mockhttp.Request().Method(http.MethodHead).URLPathf(path3), 505},

		{http.MethodConnect, path1, mockhttp.Request().Method(http.MethodConnect).URLPathf(path1), 206},
		{http.MethodOptions, path2, mockhttp.Request().Method(http.MethodOptions).URLPathf(path2), 406},
		{http.MethodTrace, path3, mockhttp.Request().Method(http.MethodTrace).URLPathf(path3), 506},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d_%s", i, tc.method), func(t *testing.T) {
			m.MustMock(tc.mock.Reply(mockhttp.Status(tc.status)))

			req, _ := http.NewRequest(tc.method, m.URL(tc.path), nil)
			res, err := client.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)

			other := http.MethodGet
			if tc.method == http.MethodGet {
				other = http.MethodPost
			}

			req, _ = http.NewRequest(other, m.URL(tc.path), nil)
			res, err = client.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

			m.Clean(t.Context())
		})
	}
}
