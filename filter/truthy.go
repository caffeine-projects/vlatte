package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Truthy checks if the incoming request value is true.
func Truthy() vlatte.Filter {
	return &filtertype.TruthyMatcher{}
}
