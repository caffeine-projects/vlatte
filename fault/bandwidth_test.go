package fault_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestBandwidth_SendsEachWriteAtTheRate(t *testing.T) {
	// A slow link, deterministically: each byte comes when the rate lets it, in flushed pieces of a tenth of a second,
	// so a client's read timeout meets a known pace.
	tests := []struct {
		name   string
		rate   int
		writes []string
		want   []string
	}{
		{
			name:   "pieces of a byte",
			rate:   10,
			writes: []string{"abc"},
			want: []string{
				"a at 100ms", "flush at 100ms",
				"b at 200ms", "flush at 200ms",
				"c at 300ms", "flush at 300ms",
				"close at 300ms",
			},
		},
		{
			name:   "a last piece shorter than the others comes sooner",
			rate:   100,
			writes: []string{strings.Repeat("a", 10) + strings.Repeat("b", 10) + "ccccc"},
			want: []string{
				"aaaaaaaaaa at 100ms", "flush at 100ms",
				"bbbbbbbbbb at 200ms", "flush at 200ms",
				"ccccc at 250ms", "flush at 250ms",
				"close at 250ms",
			},
		},
		{
			name:   "writes one after the other",
			rate:   10,
			writes: []string{"a", "b"},
			want:   []string{"a at 100ms", "flush at 100ms", "b at 200ms", "flush at 200ms", "close at 200ms"},
		},
		{
			name:   "a rate under 10 bytes a second sends a byte a piece",
			rate:   5,
			writes: []string{"ab"},
			want:   []string{"a at 200ms", "flush at 200ms", "b at 400ms", "flush at 400ms", "close at 400ms"},
		},
		{
			name: "an empty answer ends at once",
			rate: 10,
			want: []string{"close at 0s"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Bandwidth(tc.rate)})

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

func TestBandwidth_APauseDoesntSpeedTheNextWriteUp(t *testing.T) {
	// The rate holds for each write: an idle second earns no burst, as a link has no store of time unused.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Bandwidth(10)})

		_, err := flow.Write([]byte("a"))
		require.NoError(t, err)

		time.Sleep(time.Second)

		_, err = flow.Write([]byte("b"))
		require.NoError(t, err)
		require.Equal(t, []string{"a at 100ms", "flush at 100ms", "b at 1.2s", "flush at 1.2s"}, sink.log)
	})
}

func TestBandwidth_PassesFlushesOnAsTheyCome(t *testing.T) {
	// A flush, such as of a head before its body, isn't slowed: it carries no bytes.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Bandwidth(1)})

		require.NoError(t, flow.Flush())
		require.NoError(t, flow.Close())
		require.Equal(t, []string{"flush at 0s", "close at 0s"}, sink.log)
	})
}

func TestBandwidth_CommonBandwidthsAreInBytesPerSecond(t *testing.T) {
	// A constant names a link by its bits per second, as links are sold: Bandwidth takes bytes, so a second of a link
	// carries an eighth of its bits.
	tests := []struct {
		name          string
		bandwidth     int
		kbitPerSecond int
	}{
		{name: "GPRS", bandwidth: fault.BandwidthGPRS, kbitPerSecond: 50},
		{name: "dial-up", bandwidth: fault.BandwidthDialUp, kbitPerSecond: 56},
		{name: "2G", bandwidth: fault.Bandwidth2G, kbitPerSecond: 250},
		{name: "good 2G", bandwidth: fault.BandwidthGood2G, kbitPerSecond: 450},
		{name: "3G", bandwidth: fault.Bandwidth3G, kbitPerSecond: 750},
		{name: "good 3G", bandwidth: fault.BandwidthGood3G, kbitPerSecond: 1500},
		{name: "DSL", bandwidth: fault.BandwidthDSL, kbitPerSecond: 2000},
		{name: "4G", bandwidth: fault.Bandwidth4G, kbitPerSecond: 4000},
		{name: "Wi-Fi", bandwidth: fault.BandwidthWiFi, kbitPerSecond: 30000},
		{name: "fiber", bandwidth: fault.BandwidthFiber, kbitPerSecond: 100000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.kbitPerSecond*1000/8, tc.bandwidth)

			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				flow := vlatte.NewFlow(t.Context(), failingSink{}, []vlatte.Step{fault.Bandwidth(tc.bandwidth)})

				_, err := flow.Write(make([]byte, 2*tc.bandwidth))
				require.NoError(t, err)
				require.Equal(t, 2*time.Second, time.Since(start))
			})
		})
	}
}

func TestBandwidth_StopsWhenTheClientLeaves(t *testing.T) {
	// A client that leaves gets nothing more, and the writer doesn't wait out the rest of the write.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		sink := &timedSink{start: time.Now()}
		flow := vlatte.NewFlow(ctx, sink, []vlatte.Step{fault.Bandwidth(10)})

		time.AfterFunc(150*time.Millisecond, cancel)

		n, err := flow.Write([]byte("abc"))
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, n)
		require.Equal(t, []string{"a at 100ms", "flush at 100ms"}, sink.log)
		require.Equal(t, 150*time.Millisecond, time.Since(sink.start))
	})
}

func TestBandwidth_AClientThatWentAwayEndsIt(t *testing.T) {
	// A piece the client can't get, or can't be flushed to it, ends the write at once: the rest has nowhere to go.
	errGone := errors.New("client: gone")

	for name, sink := range map[string]failingSink{
		"a write": {err: errGone, writes: true},
		"a flush": {err: errGone, flushes: true},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.Bandwidth(10)})

				_, err := flow.Write([]byte("abc"))
				require.ErrorIs(t, err, errGone)
				require.Equal(t, 100*time.Millisecond, time.Since(start))
			})
		})
	}
}

func TestBandwidth_RefusesNoRate(t *testing.T) {
	// A link that sends nothing is a mistake of the mock, not a slow link: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: bandwidth needs a byte per second at least, not 0", func() { fault.Bandwidth(0) })
	require.PanicsWithValue(t, "fault: bandwidth needs a byte per second at least, not -1", func() { fault.Bandwidth(-1) })
}

func TestBandwidth_AHugeWriteStaysPaced(t *testing.T) {
	// A body past 9.2 GB in one write, as an in-memory reply sends it, still takes its time at the rate: its bytes
	// mustn't go out at once for an overflow. Such a body can't be written in a test, so its timing is tested alone.
	tests := []struct {
		name string
		n    int
		rate int
		want time.Duration
	}{
		{name: "a small write", n: 25, rate: 100, want: 250 * time.Millisecond},
		{name: "past what n·1s holds", n: 9_223_372_037, rate: fault.BandwidthFiber, want: 737_869_762_960 * time.Nanosecond},
		{name: "a rate past what 1s·rate holds", n: 1, rate: math.MaxInt64, want: 0},
		{name: "longer than a duration holds", n: math.MaxInt64, rate: 1, want: math.MaxInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, fault.SendTime(tc.n, tc.rate))
		})
	}
}
