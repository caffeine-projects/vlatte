package filtertype

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualMatcher passes when the value equals the expected value (lenient comparison).
type EqualMatcher struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *EqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if internal.EqualValues(m.Expected, v, true) {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"Equal(", mfmt.Stringify(m.Expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eq", m.Expected)
}
