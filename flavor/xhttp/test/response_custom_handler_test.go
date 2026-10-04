package test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerReply(t *testing.T) {
	msg := "hello world"

	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("content-type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(msg))
	}

	m := xhttp.New()
	m.MustStart()

	defer m.Close()

	m.MustMock(xhttp.Get(filter.URLPath("/test")).
		Reply(xhttp.Handler(fn)),
	)

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	req.Header.Add("test", "hello")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	txt, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, res.StatusCode, http.StatusCreated)
	assert.True(t, strings.Contains(string(txt), msg))
}
