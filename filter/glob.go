package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GlobMatch matches the value against a glob pattern.
func GlobMatch(pattern string) vlatte.Filter {
	return &filtertype.GlobMatcher{Pattern: pattern}
}
