package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Split splits the value by separator and matches with the inner matcher.
type Split struct {
	Separator string
	Matcher   vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *Split) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("split: type %T is not supported. accepted types: string", v))
	}

	result, _, err := m.Matcher.Match(strings.Split(txt, m.Separator))
	if err != nil {
		return Error(fmt.Errorf("split: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Split(", m.Separator, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Split) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("split", m.Separator, Describe(m.Matcher))
}
