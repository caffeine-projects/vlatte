package filtertype

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// URLPath applies the inner matcher to the URL path of the value.
type URLPath struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *URLPath) Match(arg vlatte.FilterArg) (bool, error) {
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
			return Error(fmt.Errorf("urlpath: error parsing url: %w", err))
		}

		value = u.Path
	case fmt.Stringer:
		u, err := url.Parse(e.String())
		if err != nil {
			return Error(fmt.Errorf("urlpath: error parsing url: %w", err))
		}

		value = u.Path
	default:
		return Error(fmt.Errorf("urlpath: it only accepts the types: *url.URL | url.URL | string. got: %T", v))
	}

	ok, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), value))
	if err != nil {
		return Error(fmt.Errorf("urlpath: %w", err))
	}

	if ok {
		return Success()
	}

	return Mismatch(strings.Join([]string{"URLPath(", value, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *URLPath) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("urlPath", Describe(m.Matcher))
}
