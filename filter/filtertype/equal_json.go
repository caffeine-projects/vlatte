package filtertype

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualJSONMatcher passes when the value equals the expected JSON value.
type EqualJSONMatcher struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *EqualJSONMatcher) Match(v any) (bool, vlatte.Reason, error) {
	var expJSON []byte
	var err error

	switch vv := m.Expected.(type) {
	case string:
		expJSON = []byte(vv)
	default:
		expJSON, err = json.Marshal(m.Expected)
		if err != nil {
			return internal.Error(fmt.Errorf("equal_json: %w", err))
		}
	}

	var exp any
	err = json.Unmarshal(expJSON, &exp)
	if err != nil {
		return internal.Error(fmt.Errorf("equal_json: %w", err))
	}

	switch vv := v.(type) {
	case string:
		b := new(any)
		err = json.Unmarshal([]byte(vv), b)
		if err != nil {
			return internal.Error(fmt.Errorf("equal_json: %w", err))
		}

		if internal.EqualValues(*b, exp, false) {
			return internal.Success()
		}
	default:
		if internal.EqualValues(v, exp, false) {
			return internal.Success()
		}
	}

	return internal.Mismatch(strings.Join([]string{"EqualJSON(", mfmt.Stringify(v), ") Got: ", string(expJSON)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualJSONMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqj", m.Expected)
}
