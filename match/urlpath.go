package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// URLPath compares the URL path with the expected value (glob match).
func URLPath(expected string) vlatte.Matcher {
	return URLPathMatch(GlobMatch(expected))
}

// URLPathf compares the URL path with the formatted expected value.
func URLPathf(format string, a ...any) vlatte.Matcher {
	return URLPathMatch(GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPathMatch applies the provided matcher to the URL path.
func URLPathMatch(matcher vlatte.Matcher) vlatte.Matcher {
	return &urlPathMatcher{Matcher: matcher}
}
