package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualIgnoreCase passes when the string value equals expected ignoring case.
type EqualIgnoreCase struct {
	Expected string
}

// Name implements vlatte.Filter.
func (m *EqualIgnoreCase) Name() string { return "EqualIgnoreCase" }

// Match implements vlatte.Filter.
func (m *EqualIgnoreCase) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt := ""
	if v != nil {
		var ok bool
		txt, ok = v.(string)
		if !ok {
			return Error(fmt.Errorf("eqi: value must be a string. got: %T", v))
		}
	}

	if strings.EqualFold(m.Expected, txt) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Eqi(", m.Expected, ") Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualIgnoreCase) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eqi", m.Expected)
}
