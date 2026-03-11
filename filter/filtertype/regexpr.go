package filtertype

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// RegExp passes when the regular expression matches the value.
type RegExp struct {
	Expression string
	rg         *regexp.Regexp
	mu         sync.Mutex
}

// Match implements vlatte.Filter.
func (m *RegExp) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("regexp: it only works with string values. got: %T", v))
	}

	if m.rg == nil {
		m.mu.Lock()
		if m.rg == nil {
			r, err := regexp.Compile(m.Expression)
			if err != nil {
				m.mu.Unlock()
				return Error(fmt.Errorf("regexp: error compiling expression %s. %w", m.Expression, err))
			}

			m.rg = r
		}
		m.mu.Unlock()
	}

	match := m.rg.Match([]byte(txt))
	if match {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Match(", m.Expression, ") Expression did not match. Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *RegExp) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("regexp", m.Expression)
}
