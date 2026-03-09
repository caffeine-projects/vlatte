package filtertype

import (
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualStrictMatcher passes when the value strictly equals the expected (reflect.DeepEqual).
type EqualStrictMatcher struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *EqualStrictMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if reflect.DeepEqual(m.Expected, v) {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"StrictEq(", mfmt.Stringify(m.Expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualStrictMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqs", m.Expected)
}
