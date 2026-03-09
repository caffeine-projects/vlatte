package filter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type equalJSONMatcher struct {
	expected any
}

func (m *equalJSONMatcher) Match(v any) (vlatte.Result, error) {
	var expJSON []byte
	var err error

	switch vv := m.expected.(type) {
	case string:
		expJSON = []byte(vv)
	default:
		expJSON, err = json.Marshal(m.expected)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("equal_json: %w", err)
		}
	}

	var exp any
	err = json.Unmarshal(expJSON, &exp)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("equal_json: %w", err)
	}

	switch vv := v.(type) {
	case string:
		b := new(any)
		err = json.Unmarshal([]byte(vv), b)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("equal_json: %w", err)
		}

		if equalValues(*b, exp, false) {
			return vlatte.Result{Pass: true}, nil
		}
	default:
		if equalValues(v, exp, false) {
			return vlatte.Result{Pass: true}, nil
		}
	}

	return vlatte.Result{
		Message: strings.Join([]string{"EqualJSON(", mfmt.Stringify(v), ") Got: ", string(expJSON)}, ""),
	}, nil
}

func (m *equalJSONMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqj", m.expected)
}

// EqualJSON matches JSON values.
func EqualJSON(expected any) vlatte.Filter {
	return &equalJSONMatcher{expected: expected}
}

// Eqj matches JSON values.
func Eqj(expected any) vlatte.Filter {
	return &equalJSONMatcher{expected: expected}
}
