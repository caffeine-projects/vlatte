package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// lessMatcher passes if the value is less than the expected value.
type lessMatcher struct {
	Expected float64
}

// Name implements vlatte.Matcher.
func (m *lessMatcher) Name() string { return "Less" }

// Match implements vlatte.Matcher.
func (m *lessMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return failure(fmt.Errorf("lt: type %T is not supported. value must be compatible with float64. %w", v, err))
	}

	if vv < m.Expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Lt(", anyconv.Stringify(m.Expected), ") Value is ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *lessMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lt", m.Expected)
}
