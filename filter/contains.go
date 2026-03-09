package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Contain passes when the expected value is contained in the incoming value.
func Contain(expected any) *filtertype.ContainsMatcher {
	return &filtertype.ContainsMatcher{Expected: expected}
}

// Containf is shorthand to format the expected value.
func Containf(format string, a ...any) *filtertype.ContainsMatcher {
	return &filtertype.ContainsMatcher{Expected: fmt.Sprintf(format, a...)}
}
