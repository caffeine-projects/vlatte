package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Contain passes when the expected value is contained in the incoming value.
func Contain(expected any) vlatte.Matcher {
	return &containsMatcher{Expected: expected}
}

// Containf is shorthand to format the expected value.
func Containf(format string, a ...any) vlatte.Matcher {
	return &containsMatcher{Expected: fmt.Sprintf(format, a...)}
}
