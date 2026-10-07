package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Falsy matches a value that reads as false the way strconv.ParseBool reads text: false, "false", "f", "0" or 0.
// Nil, and values that don't read as a bool, don't match.
func Falsy() vlatte.Matcher {
	return &falsyMatcher{}
}
