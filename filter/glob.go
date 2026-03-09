package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GlobMatch matches the value against a glob pattern.
func GlobMatch(pattern string) *filtertype.GlobMatcher {
	return &filtertype.GlobMatcher{Pattern: pattern}
}
