package filter

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) MatcherFunc {
	return func(v any) (Result, error) {
		if predicate(v) {
			return Result{Pass: true}, nil
		}

		return Result{Message: "F() Wrapped predicate did not match"}, nil
	}
}
