package match

import (
	"github.com/caffeine-projects/vlatte"
)

// LessThan matches a number, or text that reads as one, less than expected.
// Numbers compare by their exact value, whatever their Go types; other values don't match.
func LessThan[N Number](expected N) vlatte.Matcher {
	return &lessMatcher{Expected: expected}
}
