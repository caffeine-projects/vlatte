package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// EqualIgnoreCase compares the expected value with the incoming request value ignoring the case.
func EqualIgnoreCase(expected string) vlatte.Matcher {
	return &equalIgnoreCaseMatcher{Expected: expected}
}

// EqualIgnoreCasef is short-hand to format the expected value.
func EqualIgnoreCasef(format string, a ...any) vlatte.Matcher {
	return &equalIgnoreCaseMatcher{Expected: fmt.Sprintf(format, a...)}
}

// EQI is an alias to EqualIgnoreCase.
func EQI(expected string) vlatte.Matcher {
	return &equalIgnoreCaseMatcher{Expected: expected}
}
