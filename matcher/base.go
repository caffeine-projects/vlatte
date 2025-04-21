package matcher

import (
	"github.com/caffeine-projects/vlatte/std/descriptor"
)

var _ Matcher = MatcherFunc(nil)

// Matcher matches values.
type Matcher interface {
	Match(v any) (Result, error)
}

// Result represents a Matcher expected.
type Result struct {
	Pass    bool   // Pass defines if Matcher passed or not.
	Message string // Message describes why the associated Matcher did not pass.
}

type MatcherFunc func(v any) (Result, error)

func (f MatcherFunc) Match(v any) (Result, error) { return f(v) }

// OnMockSent describes a Matcher that has post processes that need to be executed after the matched mock is served.
// Dedicated for stateful matchers.
// TODO: consider moving this to a specific package for extensions. example: <ROOT>/ext
type OnMockSent interface {
	OnMockSent() error
}

func success() Result                { return Result{Pass: true, Message: ""} }
func mismatch(message string) Result { return Result{Pass: false, Message: message} }

func describe(m Matcher) any {
	if sd, ok := m.(descriptor.Self); ok {
		return sd.Describe()
	}

	return Anything().(descriptor.Self).Describe()
}
