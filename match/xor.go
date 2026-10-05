package match

import (
	"github.com/caffeine-projects/vlatte"
)

// XOR matches when exactly one of the two filters passes.
func XOR(first, second vlatte.Matcher) vlatte.Matcher {
	return &xorMatcher{First: first, Second: second}
}
