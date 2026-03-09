package filtertype

import (
	"fmt"
	"strings"

	"github.com/ryanuber/go-glob"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// GlobMatcher passes when the value matches the glob pattern.
type GlobMatcher struct {
	Pattern string
}

// Match implements vlatte.Filter.
func (m *GlobMatcher) Match(v any) (bool, vlatte.Reason, error) {
	text, ok := v.(string)
	if !ok {
		return internal.Error(fmt.Errorf("glob: it only works with string types. got %T", v))
	}

	if glob.Glob(m.Pattern, text) {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"Glob(", m.Pattern, ") ", text}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *GlobMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.Pattern)
}
