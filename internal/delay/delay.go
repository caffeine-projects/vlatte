// Package delay shares delay evaluation and mock-file representation across protocols and faults.
package delay

import (
	"context"
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

// Next evaluates d without waiting and rejects negative outputs, including those of custom implementations.
func Next(ctx context.Context, d vlatte.Delay) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if d == nil {
		return 0, nil
	}

	n, err := d.Next(ctx)
	if err != nil {
		return 0, fmt.Errorf("delay: sampling: %w", err)
	}

	if n < 0 {
		return 0, fmt.Errorf("delay: expected a nonnegative duration, not %s: return 0 or a positive duration", n)
	}

	return n, nil
}

// Wait evaluates d once and waits for its duration, unless the evaluation fails or ctx ends.
func Wait(ctx context.Context, d vlatte.Delay) error {
	n, err := Next(ctx, d)
	if err != nil {
		return err
	}

	if n == 0 {
		return ctx.Err()
	}

	return httpx.Wait(ctx, n)
}

// Doc describes d without evaluating it. Fixed delays use the scalar shorthand; all others keep their descriptor,
// including its error. Call only for a non-nil delay.
func Doc(d vlatte.Delay) any {
	desc := d.Describe()
	if err := desc.Validate(); err != nil {
		return vlatte.DescribeFail("delay", err)
	}
	if props, ok := desc.Props.(map[string]any); ok && props["type"] == "fixed" && len(props) == 2 {
		if duration, ok := props["duration"].(string); ok {
			return duration
		}
	}

	return desc
}
