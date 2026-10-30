package test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// do sends req and reads the whole response, closing its body even when reading fails.
func do(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res, body
}
