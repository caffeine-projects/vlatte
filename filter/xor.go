package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// XOR matches when exactly one of the two filters passes.
func XOR(first, second vlatte.Filter) *filtertype.Xor {
	return &filtertype.Xor{First: first, Second: second}
}
