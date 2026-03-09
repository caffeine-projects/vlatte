package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Anything is an empty matcher that always passes.
func Anything() vlatte.Filter {
	return &filtertype.AnythingMatcher{}
}
