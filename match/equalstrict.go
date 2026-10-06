package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// StrictEqual matches a value equal to expected without reading text, as Equal does otherwise:
// text never equals a number or a bool. Numbers still compare by their exact value, and lists by their items.
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
