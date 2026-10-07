package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Contain matches text holding expected, when expected is text; a list holding an item equal to expected, by Equal's
// rule; or an object holding a key equal to expected.
func Contain(expected any) vlatte.Matcher {
	return &containsMatcher{Expected: expected}
}

// Containf is shorthand to format the expected value.
func Containf(format string, a ...any) vlatte.Matcher {
	return &containsMatcher{Expected: fmt.Sprintf(format, a...)}
}
