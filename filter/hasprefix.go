package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type hasPrefixMatcher struct {
	prefix string
}

func (m *hasPrefixMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("has_prefix: it only works with string type. got %T", v)
	}
	if strings.HasPrefix(txt, m.prefix) {
		return success()
	}

	return mismatch(strings.Join([]string{"HasPrefix(", m.prefix, ") Prefix is not present. Got: ", txt}, ""))
}

func (m *hasPrefixMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasPrefix", m.prefix)
}

// HasPrefix passes if the matcher argument starts with the given prefix.
func HasPrefix(prefix string) vlatte.Filter {
	return &hasPrefixMatcher{prefix: prefix}
}
