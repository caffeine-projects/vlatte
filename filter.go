package vlatte

// Filter matches values.
type Filter interface {
	Match(v any) (Result, error)
}

// Result represents a matcher result.
type Result struct {
	Pass    bool   // Pass defines if matcher passed or not.
	Message string // Message describes why the associated matcher did not pass.
}

type FilterFunc func(v any) (Result, error)

func (f FilterFunc) Match(v any) (Result, error) { return f(v) }

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}
