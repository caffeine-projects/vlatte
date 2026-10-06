package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// equalJSONMatcher passes when the JSON of the value equals the expected JSON.
type equalJSONMatcher struct {
	Expected any
	want     any // what the expected JSON means
}

// Name implements vlatte.Matcher.
func (m *equalJSONMatcher) Name() string { return "EqualJSON" }

// Match implements vlatte.Matcher.
func (m *equalJSONMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	got, err := jsonOf(v)
	if err == nil && value.Same(m.want, got) {
		return success()
	}

	return mismatch(strings.Join([]string{"EqualJSON(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// jsonOf returns what the JSON of v means: text is read as JSON, as a server reads a request body,
// and any other value is written as JSON first.
func jsonOf(v any) (any, error) {
	if text, ok := value.Text(v); ok {
		return value.DecodeJSONLenient([]byte(text))
	}

	return value.ViaJSON(v)
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalJSONMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqj", m.Expected)
}
