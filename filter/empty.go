package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type emptyMatcher struct {
}

func (m *emptyMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return success()
	}

	result, _, err := Len(0).Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("empty: %w", err)
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Empty(", mfmt.Stringify(v), ")"}, ""))
}

func (m *emptyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("empty")
}

// Empty passes if the incoming request value is empty or has zero value.
func Empty() vlatte.Filter {
	return &emptyMatcher{}
}
