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

type describedDelay struct{ desc vlatte.Descriptor }

func (d describedDelay) Next(context.Context) (time.Duration, error) { return time.Second, nil }
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
