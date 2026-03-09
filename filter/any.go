package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Any matches when any of the given matchers pass.
func Any(matchers ...vlatte.Filter) *filtertype.AnyOf {
	return &filtertype.AnyOf{Matchers: matchers}
}
