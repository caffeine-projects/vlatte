package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// StrictEqual strictly compares the expected value with incoming request values.
func StrictEqual(expected any) vlatte.Matcher {
	return &equalStrictMatcher{Expected: expected}
}

// StrictEqualf is short-hand to format the expected value.
func StrictEqualf(format string, a ...any) vlatte.Matcher {
	return &equalStrictMatcher{Expected: fmt.Sprintf(format, a...)}
}

// EQS is an alias for StrictEqual.
func EQS(expected any) vlatte.Matcher {
	return &equalStrictMatcher{Expected: expected}
}
