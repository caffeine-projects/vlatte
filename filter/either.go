package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Either matches when either filter passes.
func Either(first, second vlatte.Filter) vlatte.Filter {
	return &filtertype.EitherMatcher{First: first, Second: second}
}
