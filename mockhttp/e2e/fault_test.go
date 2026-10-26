package e2e

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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

func TestFault_Latency(t *testing.T) {
	// The whole answer, its head included, comes the latency late, and whole.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.Latency(50*time.Millisecond, 0)).Reply(mockhttp.OK().PlainText("hello")))

	start := time.Now()
	res, err := newClient(t).Get(m.URL("/f"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_Bandwidth(t *testing.T) {
	// A body of 400 bytes at 2000 bytes a second takes 200ms at least, and arrives whole.
	want := strings.Repeat("a", 400)
	m, scoped := startFaulty(t, mockhttp.Getf("/f").Step(fault.Bandwidth(2000)).Reply(mockhttp.OK().PlainText(want)))

	start := time.Now()
	res, err := newClient(t).Get(m.URL("/f"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
	require.Equal(t, want, string(body))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_SlowClose(t *testing.T) {
	// The client has the body, but reads its end late.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").Step(fault.SlowClose(50*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello")))

	start := time.Now()
	res, err := newClient(t).Get(m.URL("/f"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	require.Equal(t, "hello", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_Timeout(t *testing.T) {
	t.Run("WireStep", func(t *testing.T) {
		// The client waits, gets no byte, not even a head, then the end of the connection.
		m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.Timeout(50*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello")))

		start := time.Now()
		got, err := rawGet(t, m, "/f")
		require.NoError(t, err)
		require.Empty(t, got)
		require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
		require.EqualValues(t, 1, scoped.Hits())
	})

	t.Run("Step", func(t *testing.T) {
		// The client waits, then gets the head, sent as the answer is cut, and the end of the connection for a body.
		m, scoped := startFaulty(t, mockhttp.Getf("/f").Step(fault.Timeout(50*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello")))

		start := time.Now()
		res, err := newClient(t).Get(m.URL("/f"))
		require.NoError(t, err)
		require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
		require.Equal(t, http.StatusOK, res.StatusCode)

		body, err := io.ReadAll(res.Body)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Empty(t, body)
		require.NoError(t, res.Body.Close())
		require.EqualValues(t, 1, scoped.Hits())
	})
}

func TestFault_ConnectionResetByPeerAfter(t *testing.T) {
	// The client reads the whole answer, then a reset where it waits for the end of the connection.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.ConnectionResetByPeerAfter(50*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello")))

	got, err := rawGet(t, m, "/f")
	require.ErrorIs(t, err, syscall.ECONNRESET)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_Slicer(t *testing.T) {
	// The answer comes in slices of 6 bytes at most, its head included, each 2ms after the one before, and the client
	// reads it whole.
	m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.Slicer(4, 2, 2*time.Millisecond)).Reply(mockhttp.OK().PlainText("hello world")))

	start := time.Now()
	got, err := rawGet(t, m, "/f")
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), time.Duration(len(got)/6)*2*time.Millisecond)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFault_LimitData(t *testing.T) {
	t.Run("WireStep", func(t *testing.T) {
		// The limit counts the bytes of the whole answer: the client gets the start of the status line, then the end.
		m, scoped := startFaulty(t, mockhttp.Getf("/f").WireStep(fault.LimitData(12)).Reply(mockhttp.OK().PlainText("hello")))

		got, err := rawGet(t, m, "/f")
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1 200", string(got))
		require.EqualValues(t, 1, scoped.Hits())
	})

	t.Run("Step", func(t *testing.T) {
		// The limit counts the bytes of the body: the client gets a valid head, then a body cut short.
		m, scoped := startFaulty(t, mockhttp.Getf("/f").Step(fault.LimitData(3)).Reply(mockhttp.OK().PlainText("hello")))

		res, err := newClient(t).Get(m.URL("/f"))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		body, err := io.ReadAll(res.Body)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Equal(t, "hel", string(body))
		require.NoError(t, res.Body.Close())
		require.EqualValues(t, 1, scoped.Hits())
	})
}

func TestFault_FromAMockFile(t *testing.T) {
	// A mock file declares faults as specs, at the level of each: they act as the faults of Go code do.
	start := func(t *testing.T, file string) (*mockhttp.Server, *vlatte.Scope[*mockhttp.Mock]) {
		t.Helper()

		m := mockhttp.MustNew(mockhttp.Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		return m, m.MustMock(mockhttp.FromFile("testdata/fault/" + file))
	}

	t.Run("wire_steps", func(t *testing.T) {
		m, scoped := start(t, "1_reset.yaml")

		_, err := newClient(t).Get(m.URL("/reset"))
		require.ErrorIs(t, err, syscall.ECONNRESET)
		require.EqualValues(t, 1, scoped.Hits())
	})

	t.Run("steps", func(t *testing.T) {
		m, scoped := start(t, "2_limit_data.yaml")

		res, err := newClient(t).Get(m.URL("/limit"))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		body, err := io.ReadAll(res.Body)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Equal(t, "hel", string(body))
		require.NoError(t, res.Body.Close())
		require.EqualValues(t, 1, scoped.Hits())
	})

	t.Run("latency", func(t *testing.T) {
		// 50ms, give or take 10ms.
		m, scoped := start(t, "3_latency.yaml")

		begin := time.Now()
		res, err := newClient(t).Get(m.URL("/latency"))
		require.NoError(t, err)
		require.GreaterOrEqual(t, time.Since(begin), 40*time.Millisecond)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, "hello", string(body))
		require.EqualValues(t, 1, scoped.Hits())
	})
}
