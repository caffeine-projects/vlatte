package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Falsy checks if the incoming request value is false.
func Falsy() vlatte.Matcher {
	return &falsyMatcher{}
}
