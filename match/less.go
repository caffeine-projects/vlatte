package match

import (
	"github.com/caffeine-projects/vlatte"
)

// LessThan passes if the value is lower than the given value.
func LessThan(expected float64) vlatte.Matcher {
	return &lessMatcher{Expected: expected}
}
