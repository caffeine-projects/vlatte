package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Trim trims spaces and matches with the inner matcher.
func Trim(matcher vlatte.Matcher) vlatte.Matcher {
	return &trimMatcher{Matcher: matcher}
}
