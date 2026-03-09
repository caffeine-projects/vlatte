package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// StrictEqual strictly compares the expected value with incoming request values.
func StrictEqual(expected any) *filtertype.EqualStrictMatcher {
	return &filtertype.EqualStrictMatcher{Expected: expected}
}

// StrictEqualf is short-hand to format the expected value.
func StrictEqualf(format string, a ...any) *filtertype.EqualStrictMatcher {
	return &filtertype.EqualStrictMatcher{Expected: fmt.Sprintf(format, a...)}
}

// Eqs is an alias for StrictEqual.
func Eqs(expected any) *filtertype.EqualStrictMatcher {
	return &filtertype.EqualStrictMatcher{Expected: expected}
}
