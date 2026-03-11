package filtertype

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Len passes when the value's length equals the expected length.
type Len struct {
	Length int
}

// Match implements vlatte.Filter.
func (m *Len) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	value := reflect.ValueOf(v)
	actual := value.Len()
	if actual == m.Length {
		return Success()
	}

	return Mismatch(strings.Join(
		[]string{"Len(", strconv.Itoa(m.Length), ") Got: ", strconv.Itoa(actual)},
		"",
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Len) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("len", m.Length)
}
