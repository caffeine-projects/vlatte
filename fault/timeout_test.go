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

func TestTimeout_SendsNothingThenEndsTheAnswer(t *testing.T) {
	// A server that hangs: the client gets no byte, whatever the reply wrote or flushed, then the connection ends
	// once the timeout passed since the answer began, not since its end.
	synctest.Test(t, func(t *testing.T) {
		var out bytes.Buffer
		start := time.Now()
		flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Pipe{fault.Timeout(time.Second)})

		time.Sleep(400 * time.Millisecond)

		n, err := flow.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
		require.NoError(t, err)
		require.Equal(t, 19, n)
		require.NoError(t, flow.Flush())

		require.ErrorIs(t, flow.Close(), vlatte.ErrAbort)
		require.Equal(t, time.Second, time.Since(start))

		cause := context.Cause(flow.Context())
		require.ErrorIs(t, cause, vlatte.ErrAbort)
		require.NotErrorIs(t, cause, vlatte.ErrReset)
		require.Empty(t, out.String())
	})
}

func TestTimeout_EndsAStreamThatDoesntEnd(t *testing.T) {
	// A reply that writes past the timeout, as a stream does, learns at its next write that the answer ended.
	synctest.Test(t, func(t *testing.T) {
		flow := vlatte.NewFlow(t.Context(), failingSink{}, []vlatte.Pipe{fault.Timeout(time.Second)})

		_, err := flow.Write([]byte("event"))
		require.NoError(t, err)

		time.Sleep(2 * time.Second)

		_, err = flow.Write([]byte("event"))
		require.ErrorIs(t, err, vlatte.ErrAbort)
		require.ErrorIs(t, flow.Close(), vlatte.ErrAbort)
	})
}

func TestTimeout_ZeroHangsUntilTheClientLeaves(t *testing.T) {
	// No timeout is a server that never answers: only the client, giving up, ends the wait.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		flow := vlatte.NewFlow(ctx, failingSink{}, []vlatte.Pipe{fault.Timeout(0)})

		time.AfterFunc(time.Hour, cancel)

		require.ErrorIs(t, flow.Close(), context.Canceled)
		require.Equal(t, time.Hour, time.Since(start))
	})
}

func TestTimeout_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that gives up before the timeout doesn't hold the server for the rest of it.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		flow := vlatte.NewFlow(ctx, failingSink{}, []vlatte.Pipe{fault.Timeout(time.Second)})

		time.AfterFunc(200*time.Millisecond, cancel)

		require.ErrorIs(t, flow.Close(), context.Canceled)
		require.Equal(t, 200*time.Millisecond, time.Since(start))
	})
}

func TestTimeout_RefusesANegativeTimeout(t *testing.T) {
	// A timeout before the answer began is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: timeout can't last -1s", func() { fault.Timeout(-time.Second) })
}
