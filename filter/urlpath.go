package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// URLPath compares the URL path with the expected value (glob match).
func URLPath(expected string) *filtertype.URLPathMatcher {
	return URLPathMatch(GlobMatch(expected))
}

// URLPathf compares the URL path with the formatted expected value.
func URLPathf(format string, a ...any) *filtertype.URLPathMatcher {
	return URLPathMatch(GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPathMatch applies the provided matcher to the URL path.
func URLPathMatch(matcher vlatte.Filter) *filtertype.URLPathMatcher {
	return &filtertype.URLPathMatcher{Matcher: matcher}
}
