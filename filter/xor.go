package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// XOR matches when exactly one of the two filters passes.
func XOR(first, second vlatte.Filter) vlatte.Filter {
	return &filtertype.XorMatcher{First: first, Second: second}
}
