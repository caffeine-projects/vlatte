package filtertype

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// LenMatcher passes when the value's length equals the expected length.
type LenMatcher struct {
	Length int
}

// Match implements vlatte.Filter.
func (m *LenMatcher) Match(v any) (bool, vlatte.Reason, error) {
	value := reflect.ValueOf(v)
	actual := value.Len()
	if actual == m.Length {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join(
		[]string{"Len(", strconv.Itoa(m.Length), ") Got: ", strconv.Itoa(actual)},
		"",
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *LenMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("len", m.Length)
}
