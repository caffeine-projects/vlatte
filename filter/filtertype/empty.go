package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Empty passes when the value is empty or has zero length.
type Empty struct{}

// Name implements vlatte.Filter.
func (m *Empty) Name() string { return "Empty" }

// Match implements vlatte.Filter.
func (m *Empty) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return Success()
	}

	lenMatcher := &Len{Length: 0}
	ok, err := lenMatcher.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("empty: %w", err))
	}

	if ok {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Empty(", anyconv.Stringify(v), ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Empty) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("empty")
}
