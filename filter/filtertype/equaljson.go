package filtertype

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// EqualJSON passes when the value equals the expected JSON value.
type EqualJSON struct {
	Expected any
}

// Name implements vlatte.Filter.
func (m *EqualJSON) Name() string { return "EqualJSON" }

// Match implements vlatte.Filter.
func (m *EqualJSON) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	var expJSON []byte
	var err error

	switch vv := m.Expected.(type) {
	case string:
		expJSON = []byte(vv)
	default:
		expJSON, err = json.Marshal(m.Expected)
		if err != nil {
			return Error(fmt.Errorf("equal_json: %w", err))
		}
	}

	var exp any
	err = json.Unmarshal(expJSON, &exp)
	if err != nil {
		return Error(fmt.Errorf("equal_json: %w", err))
	}

	switch vv := v.(type) {
	case string:
		b := new(any)
		err = json.Unmarshal([]byte(vv), b)
		if err != nil {
			return Error(fmt.Errorf("equal_json: %w", err))
		}

		if equalutil.EqualValues(*b, exp, false) {
			return Success()
		}
	default:
		if equalutil.EqualValues(v, exp, false) {
			return Success()
		}
	}

	return Mismatch(strings.Join([]string{"EqualJSON(", string(expJSON), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualJSON) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqj", m.Expected)
}
