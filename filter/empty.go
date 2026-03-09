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

func (m *emptyMatcher) Match(v any) (vlatte.Result, error) {
	if v == nil {
		return vlatte.Result{Pass: true}, nil
	}

	result, err := Len(0).Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("empty: %w", err)
	}

	return vlatte.Result{result.Pass, strings.Join([]string{"Empty(", mfmt.Stringify(v), ")"}, "")}, nil
}

func (m *emptyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("empty")
}

// Empty passes if the incoming request value is empty or has zero value.
func Empty() vlatte.Filter {
	return &emptyMatcher{}
}
