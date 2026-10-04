package xhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdmin_MalformedMockIsBadRequest(t *testing.T) {
	m := New()
	m.MustStart()
	t.Cleanup(m.Close)

	// Trailer values must be strings. The schema does not cover trailer, so only the builder rejects this.
	body := `{"request":{"method":"GET","path":"/x"},"response":{"status":200,"trailer":{"x":[1]}}}`
	res, err := http.Post(m.URL(m.Config().AdminPath, "mocks"), "", strings.NewReader(body))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
