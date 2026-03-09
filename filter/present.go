package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Present matches when the value is present (non-zero).
func Present() vlatte.Filter {
	return &filtertype.BePresentMatcher{}
}
