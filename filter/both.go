package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type bothMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *bothMatcher) Match(value any) (vlatte.Result, error) {
	r1, err := m.first.Match(value)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("both: first: %w", err)
	}

	r2, err := m.second.Match(value)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("both: first: %w", err)
	}

	if r1.Pass && r2.Pass {
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

	return vlatte.Result{Message: strings.Join([]string{"Both() ", message}, "")}, nil
}

func (m *bothMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("both", describe(m.first), describe(m.second))
}

// Both passes when both the given matchers pass.
func Both(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &bothMatcher{first: first, second: second}
}
