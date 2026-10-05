package match

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
	"github.com/caffeine-projects/vlatte/match/internal/equalutil"
)

// equalJSONMatcher passes when the value equals the expected JSON value.
type equalJSONMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *equalJSONMatcher) Name() string { return "EqualJSON" }

// Match implements vlatte.Matcher.
func (m *equalJSONMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	var expJSON []byte
	var err error

	switch vv := m.Expected.(type) {
	case string:
		expJSON = []byte(vv)
	default:
		expJSON, err = json.Marshal(m.Expected)
		if err != nil {
			return failure(fmt.Errorf("equal_json: %w", err))
		}
	}

	var exp any
	err = json.Unmarshal(expJSON, &exp)
	if err != nil {
		return failure(fmt.Errorf("equal_json: %w", err))
	}

	switch vv := v.(type) {
	case string:
		b := new(any)
		err = json.Unmarshal([]byte(vv), b)
		if err != nil {
			return failure(fmt.Errorf("equal_json: %w", err))
		}

		if equalutil.EqualValues(*b, exp, false) {
			return success()
		}
	default:
		if equalutil.EqualValues(v, exp, false) {
			return success()
		}
	}

	return mismatch(strings.Join([]string{"EqualJSON(", string(expJSON), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalJSONMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqj", m.Expected)
}
