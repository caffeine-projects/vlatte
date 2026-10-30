package delay_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/delay"
)

var (
	_ vlatte.Delay = negativeDelay{}
	_ vlatte.Delay = describedDelay{}
)

type negativeDelay struct{}

func (negativeDelay) Next(context.Context) (time.Duration, error) { return -1, nil }
func (negativeDelay) Describe() vlatte.Descriptor                 { return vlatte.DescribeKeyOnly("test") }

func TestWait_TheCallerWaitsExactlyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		picks := 0
		d := vlatte.DelayFunc(func(context.Context) (time.Duration, error) { picks++; return time.Second, nil })
		require.NoError(t, delay.Wait(t.Context(), d))
		require.Equal(t, time.Second, time.Since(start))
		require.Equal(t, 1, picks)
	})
}

func TestWait_AnErrorDoesNotWaitTheReturnedDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("test: unavailable")
		start := time.Now()
		err := delay.Wait(t.Context(), vlatte.DelayFunc(func(context.Context) (time.Duration, error) { return time.Hour, cause }))
		require.ErrorIs(t, err, cause)
		require.Equal(t, time.Duration(0), time.Since(start))
	})
}

func TestWait_CancellationInterruptsTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		start := time.Now()
		err := delay.Wait(ctx, vlatte.Latency(time.Hour))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestNext_NegativeCustomOutputsCannotBecomeImmediateReplies(t *testing.T) {
	n, err := delay.Next(t.Context(), negativeDelay{})
	require.Equal(t, time.Duration(0), n)
	require.EqualError(t, err, "delay: expected a nonnegative duration, not -1ns: return 0 or a positive duration")
}

type describedDelay struct {
	desc     vlatte.Descriptor
	duration time.Duration
}

func (d describedDelay) Next(context.Context) (time.Duration, error) { return d.duration, nil }
func (d describedDelay) Describe() vlatte.Descriptor                 { return d.desc }

func TestDoc_FixedShorthandDoesNotHideAnInvalidDescriptor(t *testing.T) {
	cause := errors.New("test: cannot describe")
	desc := vlatte.DescribeFail("delay", cause)
	desc.Props = map[string]any{"type": "fixed", "duration": "1s"}
	_, err := json.Marshal(delay.Doc(describedDelay{desc: desc}))
	require.ErrorIs(t, err, cause)
}

func TestDoc_InvalidDescriptorKeyCannotBeHiddenByShorthand(t *testing.T) {
	desc := vlatte.Descriptor{Props: map[string]any{"type": "fixed", "duration": "1s"}}
	_, err := json.Marshal(delay.Doc(describedDelay{desc: desc}))
	require.ErrorIs(t, err, vlatte.ErrDescriptorKeyEmpty)
}

func TestNext_CancellationTakesPrecedenceOverACustomDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Even when a custom implementation ignores context, positive delays must observe cancellation.
	n, err := delay.Next(ctx, describedDelay{duration: time.Second})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, time.Duration(0), n)
}

func TestNext_ZeroDelaysDoNotCancelImmediateReplies(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, done := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer done()
	for name, ctx := range map[string]context.Context{"canceled": canceled, "expired": expired} {
		t.Run(name, func(t *testing.T) {
			for name, d := range map[string]vlatte.Delay{
				"nil":     nil,
				"fixed":   vlatte.Latency(0),
				"uniform": vlatte.UniformDistribution(0, 0),
				"jitter":  vlatte.Jitter(0, 0),
				"custom":  describedDelay{},
			} {
				t.Run(name, func(t *testing.T) {
					n, err := delay.Next(ctx, d)
					require.NoError(t, err)
					require.Equal(t, time.Duration(0), n)
					require.NoError(t, delay.Wait(ctx, d))
				})
			}
		})
	}
}

func TestWait_CancellationDuringAZeroSampleDoesNotDropTheReply(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := vlatte.DelayFunc(func(context.Context) (time.Duration, error) {
		cancel()
		return 0, nil
	})
	require.NoError(t, delay.Wait(ctx, d))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestWait_AZeroDurationDoesNotHideASamplingError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cause := errors.New("test: no duration available")
	d := vlatte.DelayFunc(func(context.Context) (time.Duration, error) {
		cancel()
		return 0, cause
	})
	require.ErrorIs(t, delay.Wait(ctx, d), cause)
}
