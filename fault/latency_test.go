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

// delays is a delay that picks each of its durations in turn.
func delays(d ...time.Duration) vlatte.Delay {
	return func() time.Duration {
		next := d[0]
		d = d[1:]

		return next
	}
}

func TestLatency_DelaysTheAnswerAndKeepsItsPace(t *testing.T) {
	// A distant server: each write and flush reaches the client the latency after the writer sent it, so a stream
	// keeps the time between its writes, only later.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Latency(vlatte.Latency(500 * time.Millisecond))})

		_, err := flow.Write([]byte("a"))
		require.NoError(t, err)
		require.NoError(t, flow.Flush())

		time.Sleep(time.Second)

		_, err = flow.Write([]byte("b"))
		require.NoError(t, err)
		require.NoError(t, flow.Flush())
		require.NoError(t, flow.Close())

		require.Equal(t, []string{
			"a at 500ms", "flush at 500ms",
			"b at 1.5s", "flush at 1.5s",
			"close at 1.5s",
		}, sink.log)
	})
}

func TestLatency_TheWriterDoesntWait(t *testing.T) {
	// The writer goes on at its own pace, as a server whose bytes are on the wire: only the end waits for them.
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		flow := vlatte.NewFlow(t.Context(), failingSink{}, []vlatte.Step{fault.Latency(vlatte.Latency(time.Second))})

		for range 3 {
			n, err := flow.Write([]byte("ab"))
			require.NoError(t, err)
			require.Equal(t, 2, n)
			require.NoError(t, flow.Flush())
		}

		require.Zero(t, time.Since(start))
		require.NoError(t, flow.Close())
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestLatency_AVaryingDelayKeepsTheOrder(t *testing.T) {
	// A write that would be due before the one sent ahead of it waits for it: the client gets the answer in order.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Latency(delays(300*time.Millisecond, 100*time.Millisecond, 400*time.Millisecond))})

		for _, w := range []string{"a", "b", "c"} {
			_, err := flow.Write([]byte(w))
			require.NoError(t, err)
		}

		require.NoError(t, flow.Close())
		require.Equal(t, []string{"a at 300ms", "b at 300ms", "c at 400ms", "close at 400ms"}, sink.log)
	})
}

func TestLatency_KeepsWhatItWasGiven(t *testing.T) {
	// The writer may reuse its buffer once Write returned: the bytes that go out later are those it wrote.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Latency(vlatte.Latency(time.Second))})

		b := []byte("ab")
		_, err := flow.Write(b)
		require.NoError(t, err)
		copy(b, "zz")

		require.NoError(t, flow.Close())
		require.Equal(t, []string{"ab at 1s", "close at 1s"}, sink.log)
	})
}

func TestLatency_AnEmptyAnswerEndsAtOnce(t *testing.T) {
	// Only what is sent is delayed: the end of an answer that sent nothing comes as it does.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Latency(vlatte.Latency(time.Second))})

		require.NoError(t, flow.Close())
		require.Equal(t, []string{"close at 0s"}, sink.log)
	})
}

func TestLatency_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves gets nothing of what was on its way, and the server doesn't wait it out.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.Latency(vlatte.Latency(time.Second))})

		_, err := flow.Write([]byte("a"))
		require.NoError(t, err)

		time.AfterFunc(200*time.Millisecond, cancel)

		require.ErrorIs(t, flow.Close(), context.Canceled)
		require.Empty(t, sink.log)
		require.Equal(t, 200*time.Millisecond, time.Since(sink.start))
	})
}

func TestLatency_StopsWhenTheClientLeavesAnIdleAnswer(t *testing.T) {
	// A client that leaves while nothing is on its way ends the answer too: the end doesn't wait, and sends nothing.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.Latency(vlatte.Latency(time.Second))})

		time.Sleep(time.Second)
		cancel()
		synctest.Wait()

		require.ErrorIs(t, flow.Close(), context.Canceled)
		require.Empty(t, sink.log)
		require.Equal(t, time.Second, time.Since(sink.start))
	})
}

func TestLatency_AClientThatWentAwayEndsIt(t *testing.T) {
	// Once a write or flush fails to reach the client, what follows has nowhere to go: the writer learns it at its
	// next call, and at the end.
	errGone := errors.New("client: gone")

	for name, sink := range map[string]failingSink{
		"a write": {err: errGone, writes: true},
		"a flush": {err: errGone, flushes: true},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Latency(vlatte.Latency(100 * time.Millisecond))})

				_, err := flow.Write([]byte("a"))
				require.NoError(t, err)
				require.NoError(t, flow.Flush())

				time.Sleep(time.Second)

				_, err = flow.Write([]byte("b"))
				require.ErrorIs(t, err, errGone)
				require.ErrorIs(t, flow.Flush(), errGone)
				require.ErrorIs(t, flow.Close(), errGone)
			})
		})
	}
}

func TestLatency_RefusesNoDelay(t *testing.T) {
	// A latency with no delay to pick is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: latency needs a delay", func() { fault.Latency(nil) })
}
