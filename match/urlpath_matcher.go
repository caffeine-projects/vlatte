package match

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
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
	switch e := v.(type) {
	case *url.URL:
		return e.Path, true
	case url.URL:
		return e.Path, true
	case fmt.Stringer:
		v = e.String()
	}

	text, ok := value.Text(v)
	if !ok {
		return "", false
	}

	u, err := url.Parse(text)
	if err != nil {
		return "", false
	}

	return u.Path, true
}

// Describe implements descriptor.SelfDescriptive.
func (m *urlPathMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("urlPath", describe(m.Matcher))
}
