package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// greaterOrEqualMatcher passes if the value is greater than or equal to the expected value.
type greaterOrEqualMatcher struct {
	Expected float64
}

// Name implements vlatte.Matcher.
func (m *greaterOrEqualMatcher) Name() string { return "GreaterOrEqual" }

// Match implements vlatte.Matcher.
func (m *greaterOrEqualMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return failure(fmt.Errorf(
			"gte: type %T is not supported. the value must be compatible with float64. %w",
			v,
			err,
		))
	}

	if vv >= m.Expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Gte(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *greaterOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gte", m.Expected)
}
