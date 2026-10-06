package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Truthy matches a value that reads as true the way strconv.ParseBool reads text: true, "true", "t", "1" or 1.
// Nil, and values that don't read as a bool, don't match.
func Truthy() vlatte.Matcher {
	return &truthyMatcher{}
}
