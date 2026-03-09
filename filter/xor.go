package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type xorMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *xorMatcher) Match(v any) (vlatte.Result, error) {
	a, err := m.first.Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("xor: first: %w", err)
	}

	b, err := m.second.Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("xor: second: %w", err)
	}

	if a.Pass != b.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	message := ""
	if !a.Pass {
		message = a.Message
	}
	if !b.Pass {
		if !b.Pass {
			message += " - "
		}
		message += b.Message
	}

	return vlatte.Result{Message: strings.Join([]string{"XOR()", message}, " ")}, nil
}

func (m *xorMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("xor", describe(m.first), describe(m.second))
}

// XOR is an exclusive OR matcher.
func XOR(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &xorMatcher{first: first, second: second}
}
