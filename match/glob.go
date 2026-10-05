package match

import (
	"github.com/caffeine-projects/vlatte"
)

// GlobMatch matches the value against a glob pattern.
func GlobMatch(pattern string) vlatte.Matcher {
	return &globMatcher{Pattern: pattern}
}
