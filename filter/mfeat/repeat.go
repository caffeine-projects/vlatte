package mfeat

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Repeat returns a filter that passes until the max number of matches is reached.
func Repeat(times int64) vlatte.Filter {
	return &filtertype.RepeatMatcher{Max: times}
}
