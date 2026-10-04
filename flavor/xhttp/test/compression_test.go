package test

import (
	"compress/gzip"
	"io"
	"net/http"
	"testing"

	. "github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressedResponse_GZIP(t *testing.T) {
	m := xhttp.New()
	m.MustStart()

	defer m.Close()

	m.MustMock(xhttp.Get(URLPath("/test")).
		Reply(xhttp.OK().
			BodyText("hello world").
			Gzip()))

	res, err := http.Get(m.URL() + "/test")
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	assert.True(t, res.Uncompressed)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "hello world", string(b))
}

func Test_GZIPProxiedResponse(t *testing.T) {
	p := xhttp.New()
	p.MustStart()

	defer p.Close()

	ps := p.MustMock(xhttp.Get(URLPath("/test")).
		Reply(xhttp.OK().
			BodyText("hello world").
			Gzip()))

	m := xhttp.New()
	m.MustStart()

	defer m.Close()

	ms := m.MustMock(xhttp.Get(URLPath("/test")).
		Reply(xhttp.From(p.URL())))

	httpClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	res, err := httpClient.Get(m.URL() + "/test")
	require.NoError(t, err)

	defer res.Body.Close()

	gz, err := gzip.NewReader(res.Body)
	require.NoError(t, err)

	b, err := io.ReadAll(gz)

	require.NoError(t, err)
	assert.False(t, res.Uncompressed)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "hello world", string(b))
	assert.True(t, ps.Called())
	assert.True(t, ms.Called())
}
