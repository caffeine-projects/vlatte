package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// All matches when all the given matchers pass.
func All(matchers ...vlatte.Filter) vlatte.Filter {
	return &filtertype.AllOfMatcher{Matchers: matchers}
}
