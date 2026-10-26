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

var _ vlatte.Stage = (*sliceSink)(nil)

// sliceSink keeps each write it gets apart, as the reads of a client that gets them one by one.
type sliceSink struct{ writes []string }

func (s *sliceSink) Write(b []byte) (int, error) {
	s.writes = append(s.writes, string(b))
	return len(b), nil
}

func (s *sliceSink) Flush() error { return nil }

func (s *sliceSink) Close() error { return nil }

func TestSlicer_SendsEachWriteInSlices(t *testing.T) {
	// A client that takes one read for a whole message reads part of it: each slice is flushed, a delay apart.
	tests := []struct {
		name   string
		size   int
		delay  time.Duration
		writes []string
		want   []string
	}{
		{
			name:   "slices of a size, and a shorter last one",
			size:   2,
			delay:  10 * time.Millisecond,
			writes: []string{"abcde"},
			want: []string{
				"ab at 10ms", "flush at 10ms",
				"cd at 20ms", "flush at 20ms",
				"e at 30ms", "flush at 30ms",
				"close at 30ms",
			},
		},
		{
			name:   "each write sliced on its own",
			size:   2,
			delay:  10 * time.Millisecond,
			writes: []string{"abc", "d"},
			want: []string{
				"ab at 10ms", "flush at 10ms",
				"c at 20ms", "flush at 20ms",
				"d at 30ms", "flush at 30ms",
				"close at 30ms",
			},
		},
		{
			name:   "no delay",
			size:   1,
			writes: []string{"ab"},
			want:   []string{"a at 0s", "flush at 0s", "b at 0s", "flush at 0s", "close at 0s"},
		},
		{
			name:  "an empty answer",
			size:  1,
			delay: time.Second,
			want:  []string{"close at 0s"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Slicer(tc.size, 0, tc.delay)})

				for _, w := range tc.writes {
					n, err := flow.Write([]byte(w))
					require.NoError(t, err)
					require.Equal(t, len(w), n)
				}

				require.NoError(t, flow.Close())
				require.Equal(t, tc.want, sink.log)
			})
		})
	}
}

func TestSlicer_SlicesOfVaryingSizesKeepTheBytes(t *testing.T) {
	// Sizes vary at random within the variation, so a client meets cuts anywhere, but it reads the same answer.
	sink := &sliceSink{}
	flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Slicer(4, 2, 0)})
	body := strings.Repeat("0123456789", 100)

	_, err := flow.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, flow.Close())

	require.Equal(t, body, strings.Join(sink.writes, ""))

	sizes := make(map[int]bool)
	for _, w := range sink.writes[:len(sink.writes)-1] {
		require.GreaterOrEqual(t, len(w), 2, "%q", w)
		require.LessOrEqual(t, len(w), 6, "%q", w)
		sizes[len(w)] = true
	}

	require.LessOrEqual(t, len(sink.writes[len(sink.writes)-1]), 6)
	require.Len(t, sizes, 5, "every size from 2 to 6 comes in %d slices", len(sink.writes))
}

func TestSlicer_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves gets no more slices, and the writer doesn't wait out the rest of them.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.Slicer(1, 0, 100*time.Millisecond)})

		time.AfterFunc(150*time.Millisecond, cancel)

		n, err := flow.Write([]byte("abc"))
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, n)
		require.Equal(t, []string{"a at 100ms", "flush at 100ms"}, sink.log)
		require.Equal(t, 150*time.Millisecond, time.Since(sink.start))
	})
}

func TestSlicer_AClientThatWentAwayEndsIt(t *testing.T) {
	// A slice the client can't get, or can't be flushed to it, ends the write: the rest has nowhere to go.
	errGone := errors.New("client: gone")

	for name, sink := range map[string]failingSink{
		"a write": {err: errGone, writes: true},
		"a flush": {err: errGone, flushes: true},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Slicer(1, 0, 100*time.Millisecond)})

				_, err := flow.Write([]byte("abc"))
				require.ErrorIs(t, err, errGone)
				require.Equal(t, 100*time.Millisecond, time.Since(start))
			})
		})
	}
}

func TestSlicer_RefusesSlicesThatCantBeCut(t *testing.T) {
	// A slice of no byte, or a variation that allows one, or a negative delay, is a mistake of the mock: it fails as
	// the mock is built.
	tests := []struct {
		name      string
		size      int
		variation int
		delay     time.Duration
		want      string
	}{
		{name: "no size", size: 0, want: "fault: slicer needs slices of a byte at least, not 0"},
		{name: "a negative variation", size: 2, variation: -1, want: "fault: slicer size variation must be from 0 to 1, not -1"},
		{name: "a variation as large as the size", size: 2, variation: 2, want: "fault: slicer size variation must be from 0 to 1, not 2"},
		{name: "a negative delay", size: 1, delay: -time.Second, want: "fault: slicer can't wait -1s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.PanicsWithValue(t, tc.want, func() { fault.Slicer(tc.size, tc.variation, tc.delay) })
		})
	}
}
