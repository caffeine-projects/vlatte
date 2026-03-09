package filtertype

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualMatcher passes when the value equals the expected value (lenient comparison).
type EqualMatcher struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *EqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if equalutil.EqualValues(m.Expected, v, true) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Equal(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eq", m.Expected)
}
