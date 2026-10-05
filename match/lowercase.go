package match

import (
	"github.com/caffeine-projects/vlatte"
)

// ToLower lowers the incoming request value case before submitting it to the provided matcher.
func ToLower(matcher vlatte.Matcher) vlatte.Matcher {
	return &lowerCaseMatcher{Matcher: matcher}
}
