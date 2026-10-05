package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Either matches when either filter passes.
func Either(first, second vlatte.Matcher) vlatte.Matcher {
	return &eitherMatcher{First: first, Second: second}
}
