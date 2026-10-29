package vlatte

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestLatency(t *testing.T) {
	d := 5 * time.Second
	r := Latency(d)()

	require.Equal(t, d, r)
}

func TestUniformDistribution(t *testing.T) {
	minMillis := int64(100)
	maxMillis := int64(300)

	duration := UniformDistribution(minMillis, maxMillis)()

	require.LessOrEqual(t, duration.Milliseconds(), maxMillis)
	require.GreaterOrEqual(t, duration.Milliseconds(), minMillis)
}

func TestUniformDistributionRejectsInvertedRange(t *testing.T) {
	// An inverted range is a setup mistake: fail when the delay is created, not on every request.
	require.Panics(t, func() { UniformDistribution(300, 100) })
}

func TestJitterWithoutDeviation(t *testing.T) {
	// Zero deviation means no jitter.
	require.Equal(t, 300*time.Millisecond, Jitter(300*time.Millisecond, 0)())
}

func TestJitter(t *testing.T) {
	avg := 300 * time.Millisecond
	deviation := float64(15) // total spread: values fall within avg ± 7.5%
	jitter := Jitter(avg, deviation)

	spread := time.Duration(float64(avg) * deviation / 100)
	low, high := avg-spread/2, avg+spread/2
	seen := make(map[time.Duration]struct{})

	for range 1000 {
		d := jitter()
		require.GreaterOrEqual(t, d, low)
		require.Less(t, d, high)
		seen[d] = struct{}{}
	}

	// A constant delay would also stay within bounds: jitter must vary.
	require.Greater(t, len(seen), 1)
}

func TestJitterRejectsDeviationOver200(t *testing.T) {
	// Over 200 percent, half the spread exceeds avg and Jitter would pick negative delays.
	require.Panics(t, func() { Jitter(100*time.Millisecond, 300) })
}

func TestJitterAt200SpreadsFromZeroToTwiceAvg(t *testing.T) {
	// 200 is the widest deviation allowed: the spread reaches down to zero, never below it.
	avg := 100 * time.Millisecond
	jitter := Jitter(avg, 200)

	for range 1000 {
		d := jitter()
		require.GreaterOrEqual(t, d, time.Duration(0))
		require.Less(t, d, 2*avg)
	}
}

func TestUniformDistributionStaysWithinRangeProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		minMillis := rapid.Int64Range(0, 1_000_000).Draw(t, "min")
		maxMillis := minMillis + rapid.Int64Range(0, 1_000_000).Draw(t, "width")

		d := UniformDistribution(minMillis, maxMillis)()

		require.Equal(t, time.Duration(0), d%time.Millisecond)
		require.GreaterOrEqual(t, d.Milliseconds(), minMillis)
		require.LessOrEqual(t, d.Milliseconds(), maxMillis)
	})
}

func TestJitterNeverPicksNegativeDelaysProperty(t *testing.T) {
	// The bug this guards: a mock given a negative delay answers at once, so the delays it waits are not spread
	// around avg any more.
	rapid.Check(t, func(t *rapid.T) {
		avg := genAvg().Draw(t, "avg")
		deviation := rapid.Float64Range(0, 200).Draw(t, "deviation")
		jitter := Jitter(avg, deviation)

		spread := time.Duration(float64(avg) * deviation / 100)
		low := avg - spread/2

		for range 20 {
			d := jitter()
			require.GreaterOrEqual(t, d, time.Duration(0))
			if spread <= 0 {
				require.Equal(t, avg, d)
				continue
			}

			require.GreaterOrEqual(t, d, low)
			require.Less(t, d, low+spread)
		}
	})
}

func TestJitterWithoutSpreadGivesAvgProperty(t *testing.T) {
	// A deviation of zero or below leaves no spread: the delay is avg every time.
	rapid.Check(t, func(t *rapid.T) {
		avg := genAvg().Draw(t, "avg")
		deviation := rapid.Float64Range(-1e9, 0).Draw(t, "deviation")

		require.Equal(t, avg, Jitter(avg, deviation)())
	})
}

func TestJitterRejectsAnyDeviationOver200Property(t *testing.T) {
	// The panic comes when the delay is created, so a bad setup fails at once instead of on some request.
	rapid.Check(t, func(t *rapid.T) {
		avg := genAvg().Draw(t, "avg")
		deviation := rapid.Float64Range(math.Nextafter(200, math.Inf(1)), 1e9).Draw(t, "deviation")

		require.Panics(t, func() { Jitter(avg, deviation) })
	})
}

// Generators

// genAvg draws an average delay up to a day: wide enough for any mock, and small enough that float64 holds it exactly.
func genAvg() *rapid.Generator[time.Duration] {
	return rapid.Custom(func(t *rapid.T) time.Duration {
		return time.Duration(rapid.Int64Range(0, int64(24*time.Hour)).Draw(t, "nanos"))
	})
}
