package filter

import "github.com/caffeine-projects/vlatte"

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) vlatte.FilterFunc {
	return func(v any) (vlatte.Result, error) {
		if predicate(v) {
			return vlatte.Result{Pass: true}, nil
		}

		return vlatte.Result{Message: "F() Wrapped predicate did not match"}, nil
	}
}

