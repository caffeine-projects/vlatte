package vlatte

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// Delay picks how long a mock waits before it replies. It is called for each reply, so it can pick a different
// duration each time.
type Delay func() time.Duration

// Latency returns a [Delay] of latency every time.
func Latency(latency time.Duration) Delay {
	return func() time.Duration { return latency }
}

// UniformDistribution returns a [Delay] picked at random from minMillis to maxMillis milliseconds, both included.
// It panics when maxMillis is lower than minMillis.
func UniformDistribution(minMillis, maxMillis int64) Delay {
	if maxMillis < minMillis {
		panic(fmt.Sprintf("delay: uniform distribution max (%d) must not be lower than min (%d)", maxMillis, minMillis))
	}

	return func() time.Duration {
		return time.Duration(rand.Int64N(maxMillis-minMillis+1)+minMillis) * time.Millisecond
	}
}

// Jitter returns a [Delay] picked at random around avg, spread over deviation percent of avg: with an avg of 100ms and
// a deviation of 20, it picks from 90ms to just under 110ms. A deviation that leaves no spread, such as 0, gives avg
// every time. It panics when deviation is greater than 200: the spread would then reach below zero.
func Jitter(avg time.Duration, deviation float64) Delay {
	if deviation > 200 {
		panic(fmt.Sprintf("delay: jitter deviation (%v) must not be greater than 200", deviation))
	}

	maxDeviation := time.Duration(float64(avg) * (deviation / 100.0))
	if maxDeviation <= 0 {
		return Latency(avg)
	}

	return func() time.Duration {
		jitter := time.Duration(rand.Int64N(int64(maxDeviation)))

		return avg + jitter - (maxDeviation / 2)
	}
}
