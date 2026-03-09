package vlatte

type Reason string

// Filter matches values.
type Filter interface {
	Match(v any) (bool, Reason, error)
}

type FilterFunc func(v any) (bool, Reason, error)

func (f FilterFunc) Match(v any) (bool, Reason, error) { return f(v) }

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}
