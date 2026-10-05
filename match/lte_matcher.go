package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// lessOrEqualMatcher passes if the value is less than or equal to the expected value.
type lessOrEqualMatcher struct {
	Expected float64
}

// Name implements vlatte.Matcher.
func (m *lessOrEqualMatcher) Name() string { return "LessOrEqual" }

// Match implements vlatte.Matcher.
func (m *lessOrEqualMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return failure(fmt.Errorf(
			"lte: type %T is not supported. value must be compatible with float64. %w",
			v,
			err,
		))
	}

	if vv <= m.Expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Lte(", anyconv.Stringify(m.Expected), ") Value is ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *lessOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lte", m.Expected)
}
