package vlatte

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestDelays_ZeroSpreadKeepsTheExactDuration(t *testing.T) {
	for _, tt := range []struct {
		delay Delay
		want  time.Duration
	}{
		{Latency(123), 123},
		{UniformDistribution(123, 123), 123},
		{Jitter(123, 0), 123},
		{LogNormalDistribution(123, 0, 0), 123},
		{LogNormalDistribution(math.MaxInt64, 0, 0), math.MaxInt64},
	} {
		n, err := tt.delay.Next(t.Context())
		require.NoError(t, err)
		require.Equal(t, tt.want, n)
	}
}

func TestDelays_RejectInvalidSetupBeforeServing(t *testing.T) {
	tests := []struct {
		name    string
		make    func() Delay
		message string
	}{
		{"negative fixed", func() Delay { return Latency(-1) }, "delay: expected a nonnegative latency, not -1ns"},
		{"negative uniform", func() Delay { return UniformDistribution(-1, 1) }, "delay: expected 0 <= lower <= upper, not lower -1ns and upper 1ns"},
		{"inverted uniform", func() Delay { return UniformDistribution(2, 1) }, "delay: expected 0 <= lower <= upper, not lower 2ns and upper 1ns"},
		{"negative jitter", func() Delay { return Jitter(1, -1) }, "delay: expected 0 <= jitter <= latency and a representable latency+jitter, not latency 1ns and jitter -1ns"},
		{"jitter below zero", func() Delay { return Jitter(1, 2) }, "delay: expected 0 <= jitter <= latency and a representable latency+jitter, not latency 1ns and jitter 2ns"},
		{"overflowing jitter", func() Delay { return Jitter(math.MaxInt64, 1) }, "delay: expected 0 <= jitter <= latency and a representable latency+jitter, not latency 2562047h47m16.854775807s and jitter 1ns"},
		{"zero median", func() Delay { return LogNormalDistribution(0, 1, 0) }, "delay: expected a positive median, not 0s"},
		{"negative sigma", func() Delay { return LogNormalDistribution(1, -1, 0) }, "delay: expected a finite nonnegative sigma, not -1"},
		{"NaN sigma", func() Delay { return LogNormalDistribution(1, math.NaN(), 0) }, "delay: expected a finite nonnegative sigma, not NaN"},
		{"infinite sigma", func() Delay { return LogNormalDistribution(1, math.Inf(1), 0) }, "delay: expected a finite nonnegative sigma, not +Inf"},
		{"negative cutoff", func() Delay { return LogNormalDistribution(1, 1, -1) }, "delay: expected maxValue 0 or at least median 1ns, not -1ns"},
		{"cutoff below median", func() Delay { return LogNormalDistribution(2, 1, 1) }, "delay: expected maxValue 0 or at least median 2ns, not 1ns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { require.PanicsWithValue(t, tt.message, func() { tt.make() }) })
	}
}

func TestDelays_CanceledContextReturnsNoDuration(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, d := range []Delay{Latency(time.Second), UniformDistribution(0, math.MaxInt64), Jitter(time.Second, time.Second), LogNormalDistribution(time.Second, 1, 0)} {
		n, err := d.Next(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, time.Duration(0), n)
	}
}

func TestUniformDistribution_UsesDurationBoundsWithoutOverflow(t *testing.T) {
	seen := make(map[time.Duration]bool)
	d := UniformDistribution(10, 11)
	for range 1000 {
		n, err := d.Next(t.Context())
		require.NoError(t, err)
		require.Contains(t, []time.Duration{10, 11}, n)
		seen[n] = true
	}
	require.Len(t, seen, 2)
	rapid.Check(t, func(t *rapid.T) {
		lower := time.Duration(rapid.Int64Range(0, math.MaxInt64).Draw(t, "lower"))
		upper := time.Duration(rapid.Int64Range(int64(lower), math.MaxInt64).Draw(t, "upper"))
		n, err := UniformDistribution(lower, upper).Next(context.Background())
		require.NoError(t, err)
		require.GreaterOrEqual(t, n, lower)
		require.LessOrEqual(t, n, upper)
	})
}

func TestJitter_UsesDurationOnEitherSideOfLatency(t *testing.T) {
	seen := make(map[time.Duration]bool)
	d := Jitter(100, 1)
	for range 1000 {
		n, err := d.Next(t.Context())
		require.NoError(t, err)
		require.Contains(t, []time.Duration{99, 100, 101}, n)
		seen[n] = true
	}
	require.Len(t, seen, 3)
}

func TestLogNormalDistribution_LogarithmsHaveTheRequestedMeanAndSpread(t *testing.T) {
	const count = 50000
	const sigma = 0.8
	median := 100 * time.Millisecond
	d := LogNormalDistribution(median, sigma, 0)
	var sum float64
	var squares float64
	for range count {
		n, err := d.Next(t.Context())
		require.NoError(t, err)
		x := math.Log(float64(n) / float64(median))
		sum += x
		squares += x * x
	}
	mean := sum / count
	require.InDelta(t, 0, mean, 0.035)
	require.InDelta(t, sigma, math.Sqrt(squares/count-mean*mean), 0.035)
}

func TestLogNormalDistribution_TruncatesWithoutClippingTheTail(t *testing.T) {
	median := 100 * time.Millisecond
	d := LogNormalDistribution(median, 1, median)
	for range 10000 {
		n, err := d.Next(t.Context())
		require.NoError(t, err)
		require.GreaterOrEqual(t, n, time.Duration(0))
		require.Less(t, n, median, "clipping would pile the upper half of samples onto median")
	}
}

func TestLogNormalDistribution_ExtremeParametersStayRepresentable(t *testing.T) {
	for _, d := range []Delay{
		LogNormalDistribution(math.MaxInt64, 1e-300, 0),
		LogNormalDistribution(math.MaxInt64, 1, math.MaxInt64),
		LogNormalDistribution(1, math.MaxFloat64, 0),
	} {
		for range 100 {
			n, err := d.Next(t.Context())
			require.NoError(t, err)
			require.GreaterOrEqual(t, n, time.Duration(0))
		}
	}
}

func TestDelayFunc_ErrorsDiscardTheDurationAndPreserveTheCause(t *testing.T) {
	cause := errors.New("test: no duration")
	d := DelayFunc(func(context.Context) (time.Duration, error) { return time.Hour, cause })
	n, err := d.Next(t.Context())
	require.ErrorIs(t, err, cause)
	require.Equal(t, time.Duration(0), n)
	_, err = d.Describe().MarshalJSON()
	require.ErrorIs(t, err, ErrDelayNotDescribed)

	_, err = DelayFunc(func(context.Context) (time.Duration, error) { return -1, nil }).Next(t.Context())
	require.EqualError(t, err, "delay: expected a nonnegative duration, not -1ns: return 0 or a positive duration")
}

func TestDelays_DescriptionsDoNotShareMutableConfiguration(t *testing.T) {
	d := LogNormalDistribution(time.Second, 0.5, 2*time.Second)
	first := d.Describe()
	first.Props.(map[string]any)["median"] = "0s"
	b, err := d.Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"lognormal","median":"1s","sigma":0.5,"maxValue":"2s"}`, string(b))
}

func TestDelays_CanBeSharedByConcurrentCallers(t *testing.T) {
	for _, d := range []Delay{Latency(1), UniformDistribution(0, 100), Jitter(100, 100), LogNormalDistribution(100, 1, 1000)} {
		results := make(chan error)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 100 {
					if _, err := d.Next(t.Context()); err != nil {
						results <- err
						return
					}
					if err := d.Describe().Validate(); err != nil {
						results <- err
						return
					}
				}
				results <- nil
			})
		}
		errs := make([]error, 0, 8)
		for range 8 {
			errs = append(errs, <-results)
		}
		wg.Wait()
		require.NoError(t, errors.Join(errs...))
	}
}

func TestLogNormalDistribution_PreservesTheSmallTailOfALargeMedian(t *testing.T) {
	const count = 30000
	const sigma = 30.0
	median := time.Duration(1e18)
	d := LogNormalDistribution(median, sigma, 0)
	zeros := 0
	for range count {
		n, err := d.Next(t.Context())
		require.NoError(t, err)
		if n == 0 {
			zeros++
		}
	}
	cdf := func(z float64) float64 { return (1 + math.Erf(z/math.Sqrt2)) / 2 }
	// Integer durations round samples below 1ns to zero, conditional on fitting the maximum duration.
	want := cdf(-math.Log(float64(median))/sigma) / cdf(math.Log(float64(math.MaxInt64)/float64(median))/sigma)
	require.InDelta(t, want, float64(zeros)/count, 0.015)
}

func TestDelayFunc_CancellationPreventsCallingTheFunction(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	d := DelayFunc(func(context.Context) (time.Duration, error) {
		called = true
		return time.Second, nil
	})
	n, err := d.Next(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, time.Duration(0), n)
	require.False(t, called)
}

func TestLogNormalDistribution_SamplesRespectDurationPrecisionAndBounds(t *testing.T) {
	// At this boundary the floating-point cutoff rounds up: the integer check must still reject the sample.
	roundedUpper := time.Duration(math.Expm1(40))
	for _, tt := range []struct {
		name     string
		median   time.Duration
		exponent float64
		upper    time.Duration
		want     time.Duration
		accepted bool
	}{
		{"median at maximum duration", math.MaxInt64, 0, math.MaxInt64, math.MaxInt64, true},
		{"positive sample", 100, 1, 300, 271, true},
		{"negative exponent near median", 100, -0.1, 100, 90, true},
		{"subnanosecond sample near median", 1, -0.1, 1, 0, true},
		{"small tail of a large median", 1e18, -40, math.MaxInt64, 4, true},
		{"above cutoff", 100, 1, 200, 0, false},
		{"unrepresentable sample", 1, math.Inf(1), math.MaxInt64, 0, false},
		{"rounded cutoff", 1, 40, roundedUpper, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := (lognormalDelay{median: tt.median}).sample(tt.exponent, tt.upper)
			require.Equal(t, tt.accepted, ok)
			require.Equal(t, tt.want, n)
		})
	}
}
