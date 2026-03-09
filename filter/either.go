package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type eitherMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *eitherMatcher) Match(v any) (vlatte.Result, error) {
	r1, err := m.first.Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("either: first: %w", err)
	}

	r2, err := m.second.Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("either: second: %w", err)
	}

	if r1.Pass || r2.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	message := ""
	if !r1.Pass {
		message = r1.Message
	}

	if !r2.Pass {
		if !r1.Pass {
			message += " - "
		}
		message += r2.Message
	}

	return vlatte.Result{Message: strings.Join([]string{"Either() ", message}, "")}, nil
}

func (m *eitherMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("either", describe(m.first), describe(m.second))
}

// Either passes when any of the two given matchers pass.
func Either(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &eitherMatcher{first: first, second: second}
}
