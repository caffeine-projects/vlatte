package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// EqualIgnoreCase compares the expected value with the incoming request value ignoring the case.
func EqualIgnoreCase(expected string) *filtertype.EqualIgnoreCase {
	return &filtertype.EqualIgnoreCase{Expected: expected}
}

// EqualIgnoreCasef is short-hand to format the expected value.
func EqualIgnoreCasef(format string, a ...any) *filtertype.EqualIgnoreCase {
	return &filtertype.EqualIgnoreCase{Expected: fmt.Sprintf(format, a...)}
}

// EQI is an alias to EqualIgnoreCase.
func EQI(expected string) *filtertype.EqualIgnoreCase {
	return &filtertype.EqualIgnoreCase{Expected: expected}
}
