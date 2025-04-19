package matcher

import "fmt"

// Function creates an anonymous Matcher using the given function.
func Function(fn func(v any) (bool, error)) MatcherFunc {
	return func(v any) (Result, error) {
		pass, err := fn(v)
		if err != nil {
			return Result{}, fmt.Errorf("fn: matcher: %w", err)
		}

		if pass {
			return Result{Pass: true}, nil
		}

		return Result{Message: "F() Wrapped predicate did not match"}, nil
	}
}
