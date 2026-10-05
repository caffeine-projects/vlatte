package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Anything is an empty matcher that always passes.
func Anything() vlatte.Matcher {
	return &anythingMatcher{}
}
