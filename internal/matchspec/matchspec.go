// Package matchspec reads the matcher of a value of a mock file, or of a query of the admin API, with the server's
// registry of matchers.
package matchspec

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

// MatcherOf returns the matcher of v, a value of a mock file, that r builds: text matches equal text, and a list is a
// matcher spec.
func MatcherOf(r *matchbuild.Registry, v config.Value) (vlatte.Matcher, error) {
	switch x := v.Any.(type) {
	case string:
		return r.TryBuildMatcher(x)
	case []any:
		if len(x) == 0 {
			return nil, config.Invalid(v, v.Key+" is an empty list", "name a matcher, such as [equal, x]")
		}

		return r.TryBuildMatcher(x)
	default:
		return nil, config.Mismatch(v, "a string or a matcher list, such as [equal, x]", config.String)
	}
}
