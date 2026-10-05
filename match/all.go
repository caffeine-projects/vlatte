package match

import (
	"github.com/caffeine-projects/vlatte"
)

// All matches when all the given matchers pass.
func All(matchers ...vlatte.Matcher) vlatte.Matcher {
	return &allOfMatcher{Matchers: matchers}
}
