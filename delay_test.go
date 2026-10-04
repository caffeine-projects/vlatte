package vlatte

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLatency(t *testing.T) {
	d := 5 * time.Second
	r := Latency(d)()

	require.Equal(t, d, r)
}

func TestUniformDistribution(t *testing.T) {
	min := int64(100)
	max := int64(300)

	duration := UniformDistribution(min, max)()

	require.LessOrEqual(t, duration.Milliseconds(), max)
	require.GreaterOrEqual(t, duration.Milliseconds(), min)
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
