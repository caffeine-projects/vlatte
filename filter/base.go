package filter

import (
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

var _ Filter = FilterFunc(nil)

// Filter matches values.
type Filter interface {
	Match(v any) (Result, error)
}

// Result represents a Matcher expected.
type Result struct {
	Pass    bool   // Pass defines if Matcher passed or not.
	Message string // Message describes why the associated Matcher did not pass.
}

type FilterFunc func(v any) (Result, error)

func (f FilterFunc) Match(v any) (Result, error) { return f(v) }

// Stateful describes a Matcher that has post processes that need to be executed after the matched mock is served.
// Dedicated for stateful matchers.
// TODO: consider moving this to a specific package for extensions. example: <ROOT>/ext
type Stateful interface {
	SetState() error
}

func success() Result                { return Result{Pass: true, Message: ""} }
func mismatch(message string) Result { return Result{Pass: false, Message: message} }

func describe(m Filter) any {
	if sd, ok := m.(descriptor.Self); ok {
		return sd.Describe()
	}

	return Anything().(descriptor.Self).Describe()
}
