package e2e

import (
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestExternalResponseBodies(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	client := &http.Client{}
	cases := []struct {
		mock *mockhttp.Builder
		body string
	}{
		{mockhttp.Getf("/test").
			Reply(mockhttp.OK().BodyFile("./testdata/external_body/01.txt")), "hello world\n"},
		{mockhttp.Getf("/test").
			Reply(mockhttp.OK().Gzip().BodyFile("./testdata/external_body/01.txt")), "hello world\n"},
	}

	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			m.MustMock(tc.mock)

			req, _ := http.NewRequest(http.MethodGet, m.URL("/test"), nil)
			res, err := client.Do(req)
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tc.body, string(b))

			m.Clean(t.Context())
		})
	}
}
