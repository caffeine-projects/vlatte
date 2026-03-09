package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EqualIgnoreCaseMatcher passes when the string value equals expected ignoring case.
type EqualIgnoreCaseMatcher struct {
	Expected string
}

// Match implements vlatte.Filter.
func (m *EqualIgnoreCaseMatcher) Match(v any) (bool, vlatte.Reason, error) {
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
func (m *EqualIgnoreCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eqi", m.Expected)
}
