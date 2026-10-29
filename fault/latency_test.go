package fault_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestLatency_DelaysTheAnswerAndKeepsItsPace(t *testing.T) {
	// A distant server: each write and flush reaches the client the latency after the writer sent it, so a stream
	// keeps the time between its writes, only later.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{fault.Latency(500*time.Millisecond, 0)})

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
		flow := vlatte.NewFlow(t.Context(), failingSink{}, []vlatte.Pipe{fault.Latency(time.Second, 0)})

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

func TestLatency_JitterKeepsTheOrder(t *testing.T) {
	// A write whose delay would make it due before the one sent ahead of it waits for it: the client gets the answer
	// in order, each write within the jitter of the latency, or later for that wait.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{fault.Latency(time.Second, time.Second)})

		want := make([]string, 0, 20)
		for i := range cap(want) {
			w := string(rune('a' + i))
			want = append(want, w)

			_, err := flow.Write([]byte(w))
			require.NoError(t, err)
		}

		require.NoError(t, flow.Close())
		require.Len(t, sink.log, len(want)+1)

		var last time.Duration
		for i, w := range want {
			got, at, _ := strings.Cut(sink.log[i], " at ")
			require.Equal(t, w, got)

			d, err := time.ParseDuration(at)
			require.NoError(t, err)
			require.LessOrEqual(t, d, 2*time.Second)
			require.GreaterOrEqual(t, d, last)

			last = d
		}
	})
}

func TestLatency_IsDescribedByItsDurations(t *testing.T) {
	// A latency describes itself as the spec that builds it again, from Go code as from a mock file.
	// A jitter of 0, the default, is left out.
	got, ok := vlatte.IsSelfDescriptive(fault.Latency(1500*time.Millisecond, 0))
	require.True(t, ok)
	b, err := got.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"type": "latency", "latency": "1.5s"}`, string(b))

	got, ok = vlatte.IsSelfDescriptive(fault.Latency(time.Second, 200*time.Millisecond))
	require.True(t, ok)
	b, err = got.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"type": "latency", "latency": "1s", "jitter": "200ms"}`, string(b))
}

func TestLatency_KeepsWhatItWasGiven(t *testing.T) {
	// The writer may reuse its buffer once Write returned: the bytes that go out later are those it wrote.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{fault.Latency(time.Second, 0)})

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
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{fault.Latency(time.Second, 0)})

		require.NoError(t, flow.Close())
		require.Equal(t, []string{"close at 0s"}, sink.log)
	})
}

func TestLatency_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves gets nothing of what was on its way, and the server doesn't wait it out.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Pipe{fault.Latency(time.Second, 0)})

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
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Pipe{fault.Latency(time.Second, 0)})

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
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{fault.Latency(100*time.Millisecond, 0)})

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

func TestLatency_AStageAfterItThatAbortsEndsTheFlow(t *testing.T) {
	// A stage after the latency that aborts, once the write reaches it, ends the answer then: a stream waiting on the
	// flow's context learns it without another write.
	for name, abort := range map[string]error{"an abort": vlatte.ErrAbort, "a reset": vlatte.ErrReset} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cut := vlatte.PipeFunc(func(context.Context, []byte, vlatte.Stage) (int, error) { return 0, abort })
				start := time.Now()
				flow := vlatte.NewFlow(t.Context(), failingSink{}, []vlatte.Pipe{fault.Latency(time.Second, 0), cut})

				_, err := flow.Write([]byte("a"))
				require.NoError(t, err)

				<-flow.Context().Done()
				require.Equal(t, time.Second, time.Since(start))
				require.ErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
				require.ErrorIs(t, context.Cause(flow.Context()), abort)
				require.ErrorIs(t, flow.Close(), abort)
			})
		})
	}
}

func TestLatency_RefusesANegativeDelay(t *testing.T) {
	// A delay below 0 is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: latency can't be -1s", func() { fault.Latency(-time.Second, 0) })
	require.PanicsWithValue(t, "fault: latency jitter must be from 0 to 1s, not -1ns", func() { fault.Latency(time.Second, -1) })
	require.PanicsWithValue(t, "fault: latency jitter must be from 0 to 1s, not 1.000000001s", func() {
		fault.Latency(time.Second, time.Second+1)
	})
}
