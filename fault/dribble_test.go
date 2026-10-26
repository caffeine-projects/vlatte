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

var _ vlatte.Stage = (*timedSink)(nil)

// timedSink logs each write, flush and close it gets, with the time it came, from start.
type timedSink struct {
	start time.Time
	log   []string
}

func (s *timedSink) Write(b []byte) (int, error) {
	s.log = append(s.log, string(b)+" at "+time.Since(s.start).String())
	return len(b), nil
}

func (s *timedSink) Flush() error {
	s.log = append(s.log, "flush at "+time.Since(s.start).String())
	return nil
}

func (s *timedSink) Close() error {
	s.log = append(s.log, "close at "+time.Since(s.start).String())
	return nil
}

func TestChunkedDribbleDelay_SendsTheBodyInPartsOverTime(t *testing.T) {
	// A slow network, without the randomness of a real one: a client's read timeout is tested against parts that come
	// at known times, each flushed so the client gets it then.
	tests := []struct {
		name   string
		writes []string
		want   []string
	}{
		{
			name:   "a body in parts of a size",
			writes: []string{"abc", "def"},
			want: []string{
				"ab at 0s", "flush at 0s",
				"cd at 100ms", "flush at 100ms",
				"ef at 200ms", "flush at 200ms",
				"close at 300ms",
			},
		},
		{
			name:   "a body shorter than the parts",
			writes: []string{"ab"},
			want:   []string{"a at 100ms", "flush at 100ms", "b at 200ms", "flush at 200ms", "close at 300ms"},
		},
		{
			name: "an empty body ends late",
			want: []string{"close at 300ms"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.ChunkedDribbleDelay(3, 300*time.Millisecond)})

				for _, w := range tc.writes {
					_, err := flow.Write([]byte(w))
					require.NoError(t, err)
				}

				require.NoError(t, flow.Close())
				require.Equal(t, tc.want, sink.log)
			})
		})
	}
}

func TestChunkedDribbleDelay_AFlushBeforeTheEndSendsNoBody(t *testing.T) {
	// A reply that flushes, as a stream does, sends its head: the body still waits for the end.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.ChunkedDribbleDelay(1, time.Second)})

		_, err := flow.Write([]byte("body"))
		require.NoError(t, err)
		require.NoError(t, flow.Flush())
		require.NoError(t, flow.Close())

		require.Equal(t, []string{"flush at 0s", "body at 0s", "flush at 0s", "close at 1s"}, sink.log)
	})
}

func TestChunkedDribbleDelay_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves gets nothing more, and the server doesn't wait out the rest of the delay.
	tests := []struct {
		name string
		left time.Duration
		want []string
	}{
		{name: "before a part", left: 200 * time.Millisecond, want: []string{"a at 0s", "flush at 0s"}},
		{name: "before the end", left: 700 * time.Millisecond, want: []string{"a at 0s", "flush at 0s", "b at 500ms", "flush at 500ms"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.ChunkedDribbleDelay(2, time.Second)})

				_, err := flow.Write([]byte("ab"))
				require.NoError(t, err)

				time.AfterFunc(tc.left, cancel)

				require.ErrorIs(t, flow.Close(), context.Canceled)
				require.Equal(t, tc.want, sink.log)
				require.Equal(t, tc.left, time.Since(sink.start))
			})
		})
	}
}

func TestChunkedDribbleDelay_AClientThatWentAwayEndsIt(t *testing.T) {
	// A part the client can't get, or can't be flushed to it, ends the answer at once: the rest has nowhere to go.
	errGone := errors.New("client: gone")

	for name, sink := range map[string]failingSink{
		"a write": {err: errGone, writes: true},
		"a flush": {err: errGone, flushes: true},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.ChunkedDribbleDelay(2, time.Second)})

				_, err := flow.Write([]byte("ab"))
				require.NoError(t, err)

				require.ErrorIs(t, flow.Close(), errGone)
				require.Zero(t, time.Since(start))
			})
		})
	}
}

func TestChunkedDribbleDelay_RefusesWhatCantBeSent(t *testing.T) {
	// A body in no parts, or over a negative time, is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: chunked dribble delay needs a chunk at least, not 0", func() {
		fault.ChunkedDribbleDelay(0, time.Second)
	})
	require.PanicsWithValue(t, "fault: chunked dribble delay can't last -1s", func() {
		fault.ChunkedDribbleDelay(1, -time.Second)
	})
}
