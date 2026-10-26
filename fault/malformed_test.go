package fault_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

const _head = "HTTP/1.1 404 Not Found\r\nTransfer-Encoding: chunked\r\n\r\n"

func TestMalformedResponseChunk_BreaksTheBodyAfterAValidHead(t *testing.T) {
	// The client reads the head, with the mock's status, then fails on the body: the server answered, then broke.
	tests := []struct {
		name   string
		writes []string
	}{
		{name: "a head and a chunk in one write", writes: []string{_head + "5\r\nhello\r\n"}},
		{name: "a head whose end comes in two writes", writes: []string{_head[:len(_head)-1], "\n5\r\nhello\r\n"}},
		{name: "a head whose end comes in three writes", writes: []string{_head[:len(_head)-3], "\n\r", "\n5\r\nhello\r\n"}},
		{name: "a head alone", writes: []string{_head, "5\r\nhello\r\n"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.MalformedResponseChunk()})

			for _, w := range tc.writes {
				if _, err := flow.Write([]byte(w)); err != nil {
					require.ErrorIs(t, err, vlatte.ErrAbort)
					break
				}
			}

			require.ErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
			require.True(t, bytes.HasPrefix(out.Bytes(), []byte(_head)), "%q", out.String())
			require.NotContains(t, out.String(), "hello")

			res, err := http.ReadResponse(bufio.NewReader(&out), nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, res.StatusCode)

			_, err = io.ReadAll(res.Body)
			require.ErrorContains(t, err, "invalid byte in chunk length")
		})
	}
}

func TestMalformedResponseChunk_PassesFlushesAndTheEndOn(t *testing.T) {
	// Until the head ends, the answer goes on as it is: a flush still sends it, and an answer that ends before its
	// head did isn't broken further.
	var out bytes.Buffer
	flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &bufioFlusher{w: &out}}, []vlatte.Step{fault.MalformedResponseChunk()})

	_, err := flow.Write([]byte("HTTP/1.1 200 OK\r\n"))
	require.NoError(t, err)
	require.NoError(t, flow.Flush())
	require.NoError(t, flow.Close())

	require.Equal(t, "HTTP/1.1 200 OK\r\n", out.String())
	require.NotErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
}

// bufioFlusher is a writer that can flush, as a connection's buffer can.
type bufioFlusher struct{ w io.Writer }

func (b *bufioFlusher) Write(p []byte) (int, error) { return b.w.Write(p) }

func (b *bufioFlusher) Flush() error { return nil }

var _ vlatte.Stage = failingSink{}

// failingSink is a sink whose writes, or flushes, fail with err, as those to a client that went away do.
type failingSink struct {
	err     error
	writes  bool
	flushes bool
}

func (s failingSink) Write(b []byte) (int, error) {
	if s.writes {
		return 0, s.err
	}

	return len(b), nil
}

func (s failingSink) Flush() error {
	if s.flushes {
		return s.err
	}

	return nil
}

func (s failingSink) Close() error { return nil }

func TestMalformedResponseChunk_AClientThatWentAwayEndsIt(t *testing.T) {
	// A write the client can't get fails the answer, before the head ends as when it does.
	errGone := errors.New("client: gone")

	for name, writes := range map[string][]string{
		"before the head ends": {"HTTP/1.1 200 OK\r\n"},
		"as the head ends":     {_head},
	} {
		t.Run(name, func(t *testing.T) {
			flow := vlatte.NewFlow(t.Context(), failingSink{err: errGone, writes: true}, []vlatte.Step{fault.MalformedResponseChunk()})

			_, err := flow.Write([]byte(writes[0]))
			require.ErrorIs(t, err, errGone)
			require.NotErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
		})
	}
}
