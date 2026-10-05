package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// greaterMatcher passes if the value is greater than the expected value.
type greaterMatcher struct {
	Expected float64
}

// Name implements vlatte.Matcher.
func (m *greaterMatcher) Name() string { return "Greater" }

// Match implements vlatte.Matcher.
func (m *greaterMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return failure(fmt.Errorf(
			"gt: type %T is not supported. the value must be compatible with float64. %w", v, err))
	}

	if vv > m.Expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Gt(", anyconv.Stringify(m.Expected), ") Value is ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *greaterMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gt", m.Expected)
}
