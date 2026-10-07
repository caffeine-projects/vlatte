package match

// success returns a successful match.
func success() (bool, error) {
	return true, nil
}

// mismatch returns a failed match (caller may use message for logging).
func mismatch(message string) (bool, error) {
	return false, nil
}

// failure returns a failed match with an error (e.g. type/parse errors).
func failure(err error) (bool, error) {
	return false, err
}

// anys copies items into a []any.
func anys[T any](items []T) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}

	return out
}
