package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type hasSuffixMatcher struct {
	suffix string
}

func (m *hasSuffixMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("has_suffix: it only works with string type. got %T", v)
	}

	if strings.HasSuffix(txt, m.suffix) {
		return success()
	}

	return mismatch(strings.Join([]string{"HasSuffix(", m.suffix, ") Prefix is not present. Got: ", txt}, ""))
}

func (m *hasSuffixMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasSuffix", m.suffix)
}

// HasSuffix passes when matcher argument ends with the given suffix.
func HasSuffix(suffix string) vlatte.Filter {
	return &hasSuffixMatcher{suffix: suffix}
}
