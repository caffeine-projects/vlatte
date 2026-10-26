package fault

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSendTime_AHugeWriteStaysPaced(t *testing.T) {
	// A body past 9.2 GB in one write, as an in-memory reply sends it, still takes its time at the rate: its bytes
	// mustn't go out at once for an overflow. Such a body can't be written in a test, so its timing is tested alone.
	tests := []struct {
		name string
		n    int
		rate int
		want time.Duration
	}{
		{name: "a small write", n: 25, rate: 100, want: 250 * time.Millisecond},
		{name: "past what n·1s holds", n: 9_223_372_037, rate: BandwidthFiber, want: 737_869_762_960 * time.Nanosecond},
		{name: "a rate past what 1s·rate holds", n: 1, rate: math.MaxInt64, want: 0},
		{name: "longer than a duration holds", n: math.MaxInt64, rate: 1, want: math.MaxInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sendTime(tc.n, tc.rate))
		})
	}
}
