package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Function creates an anonymous Matcher using the given function.
func Function(fn func(v any) (bool, error)) vlatte.FilterFunc {
	return func(v any) (bool, vlatte.Reason, error) {
		pass, err := fn(v)
		if err != nil {
			return false, ReasonError, fmt.Errorf("fn: matcher: %w", err)
		}

		if pass {
			return true, ReasonNone, nil
		}

		return false, vlatte.Reason("F() Wrapped predicate did not match"), nil
	}
}

