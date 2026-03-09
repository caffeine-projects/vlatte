package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Both matches when both filters pass.
func Both(first, second vlatte.Filter) vlatte.Filter {
	return &filtertype.BothMatcher{First: first, Second: second}
}
