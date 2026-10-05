package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Any matches when any of the given matchers pass.
func Any(matchers ...vlatte.Matcher) vlatte.Matcher {
	return &anyOfMatcher{Matchers: matchers}
}
