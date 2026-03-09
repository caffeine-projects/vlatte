package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Falsy checks if the incoming request value is false.
func Falsy() vlatte.Filter {
	return &filtertype.FalsyMatcher{}
}
