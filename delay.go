package vlatte

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

var (
	_ Delay = DelayFunc(nil)
	_ Delay = latencyDelay{}
	_ Delay = uniformDelay{}
	_ Delay = jitterDelay{}
	_ Delay = lognormalDelay{}
)

// ErrDelayNotDescribed is returned when describing a [DelayFunc], whose code a mock file cannot hold.
var ErrDelayNotDescribed = errors.New("delay: a function cannot be described as a mock file")

// Delay chooses a nonnegative duration without waiting for it. The caller waits with the same context and ignores
// the duration on error. Implementations must support concurrent calls and honor cancellation. Describe describes
// the configuration without sampling it, or returns a failed descriptor when no mock file can represent it.
type Delay interface {
	SelfDescriptive
	Next(ctx context.Context) (time.Duration, error)
}

// DelayFunc adapts a function to [Delay]. A function cannot be serialized: [DelayFunc.Describe] fails explicitly.
type DelayFunc func(context.Context) (time.Duration, error)

// Next calls f unless ctx has ended. A negative result is an error.
func (f DelayFunc) Next(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	d, err := f(ctx)
	if err != nil {
		return 0, err
	}

	if d < 0 {
		return 0, fmt.Errorf("delay: expected a nonnegative duration, not %s: return 0 or a positive duration", d)
	}

	return d, nil
}

// Describe returns a failed descriptor with [ErrDelayNotDescribed].
func (f DelayFunc) Describe() Descriptor { return DescribeFail("delay", ErrDelayNotDescribed) }

type latencyDelay struct {
	latency time.Duration
}

// Latency returns a [Delay] of latency every time. It panics when latency is negative.
func Latency(latency time.Duration) Delay {
	if latency < 0 {
		panic(fmt.Sprintf("delay: expected a nonnegative latency, not %s", latency))
	}

	return latencyDelay{latency: latency}
}

func (d latencyDelay) Next(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	return d.latency, nil
}

func (d latencyDelay) Describe() Descriptor {
	return DescribeKeyValuePairs("delay", "type", "fixed", "duration", d.latency.String())
}

type uniformDelay struct {
	lower time.Duration
	upper time.Duration
}

// UniformDistribution picks uniformly from lower to upper, both included, with nanosecond resolution. It panics
// when either bound is negative or upper is below lower.
func UniformDistribution(lower, upper time.Duration) Delay {
	if lower < 0 || upper < lower {
		panic(fmt.Sprintf("delay: expected 0 <= lower <= upper, not lower %s and upper %s", lower, upper))
	}

	return uniformDelay{lower: lower, upper: upper}
}

func (d uniformDelay) Next(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	return d.lower + time.Duration(rand.Uint64N(uint64(d.upper-d.lower)+1)), nil
}

func (d uniformDelay) Describe() Descriptor {
	return DescribeKeyValuePairs("delay", "type", "uniform", "lower", d.lower.String(), "upper", d.upper.String())
}

type jitterDelay struct {
	latency time.Duration
	jitter  time.Duration
}

// Jitter picks uniformly from latency-jitter to latency+jitter, both included. Unlike a percentage, jitter is the
// duration on either side of latency. It panics when latency is negative, jitter is outside [0, latency], or their
// sum overflows a duration.
func Jitter(latency, jitter time.Duration) Delay {
	if latency < 0 || jitter < 0 || jitter > latency || jitter > time.Duration(math.MaxInt64)-latency {
		panic(fmt.Sprintf("delay: expected 0 <= jitter <= latency and a representable latency+jitter, not latency %s and jitter %s", latency, jitter))
	}

	return jitterDelay{latency: latency, jitter: jitter}
}

func (d jitterDelay) Next(ctx context.Context) (time.Duration, error) {
	return uniformDelay{lower: d.latency - d.jitter, upper: d.latency + d.jitter}.Next(ctx)
}

func (d jitterDelay) Describe() Descriptor {
	return DescribeKeyValuePairs("delay", "type", "jitter", "latency", d.latency.String(), "jitter", d.jitter.String())
}

type lognormalDelay struct {
	median   time.Duration
	sigma    float64
	maxValue time.Duration
}

// LogNormalDistribution picks a lognormal delay with median and the dimensionless standard deviation sigma of its
// logarithm. Sigma 0 always returns median. A positive maxValue rejects samples above it, rather than clipping them;
// median and sigma describe the distribution before that truncation. Zero maxValue sets no explicit cutoff, but
// unrepresentable samples are rejected. Samples below a nanosecond can return 0. Rejection observes ctx cancellation.
// It panics unless median > 0, sigma is finite and nonnegative, and maxValue is 0 or at least median.
func LogNormalDistribution(median time.Duration, sigma float64, maxValue time.Duration) Delay {
	if median <= 0 {
		panic(fmt.Sprintf("delay: expected a positive median, not %s", median))
	}

	if math.IsNaN(sigma) || math.IsInf(sigma, 0) || sigma < 0 {
		panic(fmt.Sprintf("delay: expected a finite nonnegative sigma, not %v", sigma))
	}

	if maxValue < 0 || (maxValue > 0 && maxValue < median) {
		panic(fmt.Sprintf("delay: expected maxValue 0 or at least median %s, not %s", median, maxValue))
	}

	return lognormalDelay{median: median, sigma: sigma, maxValue: maxValue}
}

func (d lognormalDelay) Next(ctx context.Context) (time.Duration, error) {
	upper := d.maxValue
	if upper == 0 {
		upper = math.MaxInt64
	}

	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		if d.sigma == 0 {
			return d.median, nil
		}

		// Keep the offset separate so tiny sigma still terminates at median MaxInt64, whose float64 rounds up to an
		// unrepresentable duration. Expm1 also retains precision near zero.
		exponent := d.sigma * rand.NormFloat64()
		if exponent < -math.Ln2 {
			// Subtraction from a large median loses its small tail; this branch is at most half the median, so
			// direct multiplication is both representable and accurate.
			return time.Duration(float64(d.median) * math.Exp(exponent)), nil
		}

		offset := float64(d.median) * math.Expm1(exponent)
		if offset >= 0 {
			if offset > float64(upper-d.median) || offset >= float64(math.MaxInt64) {
				continue
			}

			n := time.Duration(offset)
			if n > upper-d.median {
				continue
			}

			return d.median + n, nil
		}

		below := math.Ceil(-offset)
		if below >= float64(d.median) {
			return 0, nil
		}

		return d.median - time.Duration(below), nil
	}
}

func (d lognormalDelay) Describe() Descriptor {
	return DescribeKeyValuePairs("delay", "type", "lognormal", "median", d.median.String(), "sigma", d.sigma,
		"maxValue", d.maxValue.String())
}
