package fault_test

import (
	"bytes"
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestConnectionResetByPeer_SendsNothingAndResets(t *testing.T) {
	// A reset is an error a client must handle apart from a connection that ends: the flow says which.
	for _, c := range _calls {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.ConnectionResetByPeer()})

			require.ErrorIs(t, c.do(flow), vlatte.ErrReset)

			cause := context.Cause(flow.Context())
			require.ErrorIs(t, cause, vlatte.ErrAbort)
			require.ErrorIs(t, cause, vlatte.ErrReset)
			require.Empty(t, out.String())
		})
	}
}

func TestConnectionResetByPeerAfter_PassesTheAnswerOnThenResets(t *testing.T) {
	// The client reads the answer as it comes, then a reset at the time set, from the start of the answer: an answer
	// that ended before is flushed whole, and the connection waits for its reset.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.ConnectionResetByPeerAfter(time.Second)})

		_, err := flow.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
		require.NoError(t, err)

		require.ErrorIs(t, flow.Close(), vlatte.ErrReset)
		require.Equal(t, []string{"HTTP/1.1 200 OK\r\n\r\n at 0s", "flush at 0s"}, sink.log)
		require.Equal(t, time.Second, time.Since(sink.start))

		cause := context.Cause(flow.Context())
		require.ErrorIs(t, cause, vlatte.ErrAbort)
		require.ErrorIs(t, cause, vlatte.ErrReset)
	})
}

func TestConnectionResetByPeerAfter_ResetsAStreamThatDoesntEnd(t *testing.T) {
	// A reply that writes past the reset, as a stream does, learns at its next write that the connection is gone.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.ConnectionResetByPeerAfter(time.Second)})

		_, err := flow.Write([]byte("a"))
		require.NoError(t, err)

		time.Sleep(2 * time.Second)

		_, err = flow.Write([]byte("b"))
		require.ErrorIs(t, err, vlatte.ErrReset)
		require.Equal(t, []string{"a at 0s"}, sink.log)
	})
}

func TestConnectionResetByPeerAfter_ZeroResetsAtOnce(t *testing.T) {
	// No time is the reset of ConnectionResetByPeer: the client gets no byte.
	for _, c := range _calls {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.ConnectionResetByPeerAfter(0)})

			require.ErrorIs(t, c.do(flow), vlatte.ErrReset)
			require.Empty(t, out.String())
		})
	}
}

func TestConnectionResetByPeerAfter_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves before the reset doesn't hold the server for the rest of the wait, and gets no reset.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		flow := vlatte.NewFlow(ctx, failingSink{}, []vlatte.Step{fault.ConnectionResetByPeerAfter(time.Second)})

		time.AfterFunc(200*time.Millisecond, cancel)

		err := flow.Close()
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, vlatte.ErrReset)
		require.Equal(t, 200*time.Millisecond, time.Since(start))
	})
}

func TestConnectionResetByPeerAfter_RefusesANegativeTime(t *testing.T) {
	// A reset before the answer began is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: connection reset by peer can't come -1s after", func() {
		fault.ConnectionResetByPeerAfter(-time.Second)
	})
}
