package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// HasPrefix passes if the value starts with the given prefix.
type HasPrefix struct {
	Prefix string
}

// Match implements vlatte.Filter.
func (m *HasPrefix) Match(v any) (bool, error) {
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("has_prefix: it only works with string type. got %T", v))
	}
	if strings.HasPrefix(txt, m.Prefix) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"HasPrefix(", m.Prefix, ") Prefix is not present. Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *HasPrefix) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasPrefix", m.Prefix)
}
