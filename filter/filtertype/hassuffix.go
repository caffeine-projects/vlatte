package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// HasSuffix passes when the value ends with the given suffix.
type HasSuffix struct {
	Suffix string
}

// Name implements vlatte.Filter.
func (m *HasSuffix) Name() string { return "HasSuffix" }

// Match implements vlatte.Filter.
func (m *HasSuffix) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("has_suffix: it only works with string type. got %T", v))
	}

	if strings.HasSuffix(txt, m.Suffix) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"HasSuffix(", m.Suffix, ") Suffix is not present. Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *HasSuffix) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasSuffix", m.Suffix)
}
