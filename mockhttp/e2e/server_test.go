package e2e

import (
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestServer_APortWithoutAHostServesTheMocks(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Addr("0"))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Getf("/tea").Reply(mockhttp.Created()))
	_, port, err := net.SplitHostPort(m.URL()[len("http://"):])
	require.NoError(t, err)
	res, err := http.Get("http://127.0.0.1:" + port + "/tea")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestServer_TLSCannotStartOnAnOccupiedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Addr(l.Addr().String()))
	t.Cleanup(m.Close)
	require.ErrorIs(t, m.StartTLS(), syscall.EADDRINUSE)
}
