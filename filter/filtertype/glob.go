package filtertype

import (
	"fmt"
	"strings"

	"github.com/ryanuber/go-glob"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Glob passes when the value matches the glob pattern.
type Glob struct {
	Pattern string
}

// Name implements vlatte.Filter.
func (m *Glob) Name() string { return "Glob" }

// Match implements vlatte.Filter.
func (m *Glob) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	text, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("glob: it only works with string types. got %T", v))
	}

	if glob.Glob(m.Pattern, text) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Glob(", m.Pattern, ") ", text}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Glob) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.Pattern)
}
