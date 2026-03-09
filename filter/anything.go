package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Anything is an empty matcher that always passes.
func Anything() *filtertype.Anything {
	return &filtertype.Anything{}
}
