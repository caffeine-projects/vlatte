package filtertype

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// HasKeyMatcher passes if the JSON key at the given path is present.
type HasKeyMatcher struct {
	Path string
	Expr jp.Expr
}

// Match implements vlatte.Filter.
func (m *HasKeyMatcher) Match(v any) (bool, vlatte.Reason, error) {
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return Error(fmt.Errorf("has_key: %s: error parsing incoming json: %w", m.Path, err))
		}

		results = m.Expr.Get(data)
	default:
		results = m.Expr.Get(vv)
	}

	size := len(results)
	if size == 0 || (size == 1 && results[0] == nil) {
		return Mismatch(strings.Join([]string{"HasKey(", m.Path, ") Is not present"}, ""))
	}

	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *HasKeyMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasKey", m.Path)
}
