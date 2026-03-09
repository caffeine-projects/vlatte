package filter

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type hasKeyMatcher struct {
	path string
	expr jp.Expr
}

func (m *hasKeyMatcher) Match(v any) (vlatte.Result, error) {
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("has_key: %s: error parsing incoming json: %w", m.path, err)
		}

		results = m.expr.Get(data)
	default:
		results = m.expr.Get(vv)
	}

	size := len(results)
	if size == 0 || (size == 1 && results[0] == nil) {
		return vlatte.Result{
			Message: strings.Join([]string{"HasKey(", m.path, ") Is not present"}, ""),
		}, nil
	}

	return vlatte.Result{Pass: true}, nil
}

func (m *hasKeyMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasKey", m.path)
}

// HasKey passes if the JSON key in the given path is present.
// Example:
//
//	JSON: { "name": "test" }
//	HasKey("name") will pass
//	HasKey("address.street") will not pass.
func HasKey(path string) vlatte.Filter {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}

	return &hasKeyMatcher{
		path: path,
		expr: x,
	}
}
