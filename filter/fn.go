package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Function creates an anonymous Matcher using the given function.
func Function(fn func(v any) (bool, error)) vlatte.FilterFunc {
	return func(v any) (vlatte.Result, error) {
		pass, err := fn(v)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("fn: matcher: %w", err)
		}

		if pass {
			return vlatte.Result{Pass: true}, nil
		}

		return vlatte.Result{Message: "F() Wrapped predicate did not match"}, nil
	}
}

