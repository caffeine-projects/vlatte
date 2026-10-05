package match

import (
	"github.com/caffeine-projects/vlatte"
)

// ToUpper uppercases the value and matches with the inner matcher.
func ToUpper(matcher vlatte.Matcher) vlatte.Matcher {
	return &upperCaseMatcher{Matcher: matcher}
}
