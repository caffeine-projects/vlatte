package fault_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestSlowClose_SendsTheAnswerThenEndsItLate(t *testing.T) {
	// The client has every byte at once, flushed, and only the end comes late: one that reads until the end waits.
	tests := []struct {
		name  string
		delay time.Duration
		want  []string
	}{
		{name: "a delay", delay: time.Second, want: []string{"body at 0s", "flush at 0s", "flush at 0s", "close at 1s"}},
		{name: "no delay", want: []string{"body at 0s", "flush at 0s", "flush at 0s", "close at 0s"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.SlowClose(tc.delay)})

				_, err := flow.Write([]byte("body"))
				require.NoError(t, err)
				require.NoError(t, flow.Flush())
				require.NoError(t, flow.Close())
				require.Equal(t, tc.want, sink.log)
			})
		})
	}
}

func TestSlowClose_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves doesn't hold the server for the rest of the delay, and gets no end.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.SlowClose(time.Second)})

		time.AfterFunc(300*time.Millisecond, cancel)

		require.ErrorIs(t, flow.Close(), context.Canceled)
		require.Equal(t, []string{"flush at 0s"}, sink.log)
		require.Equal(t, 300*time.Millisecond, time.Since(sink.start))
	})
}

func TestSlowClose_AClientThatWentAwayEndsItAtOnce(t *testing.T) {
	// An answer that can't be flushed has no one to wait for.
	errGone := errors.New("client: gone")

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		flow := vlatte.NewFlow(t.Context(), failingSink{err: errGone, flushes: true}, []vlatte.Step{fault.SlowClose(time.Second)})

		require.ErrorIs(t, flow.Close(), errGone)
		require.Zero(t, time.Since(start))
	})
}

func TestSlowClose_RefusesANegativeDelay(t *testing.T) {
	// An end before the answer is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: slow close can't last -1s", func() { fault.SlowClose(-time.Second) })
}
