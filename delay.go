package vlatte

import (
	"fmt"
	"math/rand/v2"
	"time"
)

type Delay func() time.Duration

func Latency(latency time.Duration) Delay {
	return func() time.Duration { return latency }
}

func UniformDistribution(min, max int64) Delay {
	if max < min {
		panic(fmt.Sprintf("delay: uniform distribution max (%d) must not be lower than min (%d)", max, min))
	}

	return func() time.Duration {
		return time.Duration(rand.Int64N(max-min+1)+min) * time.Millisecond
	}
}

func Jitter(avg time.Duration, deviation float64) Delay {
	maxDeviation := time.Duration(float64(avg) * (deviation / 100.0))
	if maxDeviation <= 0 {
		return Latency(avg)
	}

	return func() time.Duration {
		jitter := time.Duration(rand.Int64N(int64(maxDeviation)))

		return avg + jitter - (maxDeviation / 2)
	}
}
