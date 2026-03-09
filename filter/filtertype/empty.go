package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EmptyMatcher passes when the value is empty or has zero length.
type EmptyMatcher struct{}

// Match implements vlatte.Filter.
func (m *EmptyMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return Success()
	}

	lenMatcher := &LenMatcher{Length: 0}
	ok, _, err := lenMatcher.Match(v)
	if err != nil {
		return Error(fmt.Errorf("empty: %w", err))
	}

	if ok {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Empty(", anyconv.Stringify(v), ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EmptyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("empty")
}
