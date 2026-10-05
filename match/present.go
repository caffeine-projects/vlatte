package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Present matches when the value is present (non-zero).
func Present() vlatte.Matcher {
	return &presentMatcher{}
}
