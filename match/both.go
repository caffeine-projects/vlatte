package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Both matches when both filters pass.
func Both(first, second vlatte.Matcher) vlatte.Matcher {
	return &bothMatcher{First: first, Second: second}
}
