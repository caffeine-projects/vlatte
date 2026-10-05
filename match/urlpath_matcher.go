package match

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// urlPathMatcher applies the inner matcher to the URL path of the value.
type urlPathMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *urlPathMatcher) Name() string { return "URLPath" }

// Match implements vlatte.Matcher.
func (m *urlPathMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	var value string

	switch e := v.(type) {
	case *url.URL:
		value = e.Path
	case url.URL:
		value = e.Path
	case string:
		u, err := url.Parse(e)
		if err != nil {
			return failure(fmt.Errorf("urlpath: error parsing url: %w", err))
		}

		value = u.Path
	case fmt.Stringer:
		u, err := url.Parse(e.String())
		if err != nil {
			return failure(fmt.Errorf("urlpath: error parsing url: %w", err))
		}

		value = u.Path
	default:
		return failure(fmt.Errorf("urlpath: it only accepts the types: *url.URL | url.URL | string. got: %T", v))
	}

	ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), value))
	if err != nil {
		return failure(fmt.Errorf("urlpath: %w", err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"URLPath(", value, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *urlPathMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("urlPath", describe(m.Matcher))
}
