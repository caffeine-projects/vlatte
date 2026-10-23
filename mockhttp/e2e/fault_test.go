package e2e

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// startFaulty starts a server with mock, and returns it with the scope of the mock.
func startFaulty(t *testing.T, mock *mockhttp.Builder) (*mockhttp.Server, *vlatte.Scope[*mockhttp.Mock]) {
	t.Helper()

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	return m, m.MustMock(mock)
}

// rawGet sends a GET of path to m over a connection of its own, and returns what the server sent until it ended the
// connection, and the error that ended it: nil for an end.
func rawGet(t *testing.T, m *mockhttp.Server, path string) ([]byte, error) {
	t.Helper()

	u, err := url.Parse(m.URL(path))
	require.NoError(t, err)

	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+u.Host+"\r\n\r\n")
	require.NoError(t, err)

	return io.ReadAll(conn)
}

// newClient returns a client whose connections serve one request each, so a fault meets a fresh one.
func newClient(t *testing.T) *http.Client {
	t.Helper()

	tr := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)

	return &http.Client{Transport: tr}
}

func TestFault_EmptyResponse(t *testing.T) {
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.EmptyResponse()).Reply(mockhttp.OK().PlainText("hello")))

	_, err := newClient(t).Get(m.URL("/f"))
	require.ErrorIs(t, err, io.EOF)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_MalformedResponseChunk(t *testing.T) {
	// The client fails on the garbage before the connection ends: the call counts by that end.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.MalformedResponseChunk()).Reply(mockhttp.NotFound().PlainText("hello")))

	got, err := rawGet(t, m, "/f")
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	_, err = io.ReadAll(res.Body)
	require.ErrorContains(t, err, "invalid byte in chunk length")
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_RandomDataThenClose(t *testing.T) {
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.RandomDataThenClose()).Reply(mockhttp.OK().PlainText("hello")))

	got, err := rawGet(t, m, "/f")
	require.NoError(t, err)
	require.Len(t, got, fault.RandomDataSize)
	require.False(t, bytes.HasPrefix(got, []byte("HTTP/")))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_ConnectionResetByPeer(t *testing.T) {
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.ConnectionResetByPeer()).Reply(mockhttp.OK().PlainText("hello")))

	_, err := newClient(t).Get(m.URL("/f"))
	require.ErrorIs(t, err, syscall.ECONNRESET)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_ChunkedDribbleDelay(t *testing.T) {
	// The body arrives late and in parts, but whole.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").Step(fault.ChunkedDribbleDelay(3, 30*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello world")))

	res, err := newClient(t).Get(m.URL("/f"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello world", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}
