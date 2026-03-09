package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// IsNil matches when the value is nil.
func IsNil() vlatte.Filter {
	return &filtertype.NilMatcher{}
}
