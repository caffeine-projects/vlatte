package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Function creates an anonymous Matcher using the given function.
func Function(fn func(v any) (bool, error)) vlatte.FilterFunc {
	return func(arg vlatte.FilterArg) (bool, error) {
		pass, err := fn(arg.Value())
		if err != nil {
			return false, fmt.Errorf("fn: matcher: %w", err)
		}

		if pass {
			return true, nil
		}

		return false, nil
	}
}

