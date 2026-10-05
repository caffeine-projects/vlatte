package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Truthy checks if the incoming request value is true.
func Truthy() vlatte.Matcher {
	return &truthyMatcher{}
}
