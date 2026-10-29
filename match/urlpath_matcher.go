package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
)

// urlPathMatcher applies the inner matcher to the URL path of the value.
type urlPathMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *urlPathMatcher) Name() string { return "URLPath" }

// Match implements vlatte.Matcher.
func (m *urlPathMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	path, ok := urlPath(arg.Value())
	if !ok {
		return mismatch("URLPath() Value is not a URL")
	}

	ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), path))
	if err != nil {
		return failure(fmt.Errorf("urlpath: %w", err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"URLPath(", path, ")"}, ""))
}

// urlPath returns the path of a URL: a *url.URL or url.URL, text that parses as one, or a value whose String does.
func urlPath(v any) (string, bool) {
	u, ok := toURL(v)
	if !ok {
		return "", false
	}

	return u.Path, true
}

// Describe implements vlatte.SelfDescriptive.
func (m *urlPathMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("urlPath", describe(m.Matcher))
}
