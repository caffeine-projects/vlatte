package filter

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type regExpMatcher struct {
	expression string
	rg         *regexp.Regexp
	mu         sync.Mutex
}

func (m *regExpMatcher) Match(v any) (vlatte.Result, error) {
	txt, ok := v.(string)
	if !ok {
		return vlatte.Result{}, fmt.Errorf("regexp: it only works with string values. got: %T", v)
	}

	if m.rg == nil {
		m.mu.Lock()
		if m.rg == nil {
			r, err := regexp.Compile(m.expression)
			if err != nil {
				return vlatte.Result{}, fmt.Errorf("regexp: error compiling expression %s. %w", m.expression, err)
			}

			m.rg = r
		}
		m.mu.Unlock()
	}

	match := m.rg.Match([]byte(txt))
	if match {
		return success(), nil
	}

	return vlatte.Result{Message: strings.Join([]string{"Match(", m.expression, ") Expression did not match. Got: ", txt}, "")}, nil
}

func (m *regExpMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("regexp", m.expression)
}

// Matches passes when the given regular expression matches the incoming request value.
// It accepts a string, regexp.Regexp or *regexp.Regexp.
func Matches(expression string) vlatte.Filter {
	return &regExpMatcher{expression: expression}
}
