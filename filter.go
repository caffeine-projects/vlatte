package vlatte

import "context"

// Filter matches values.
type Filter interface {
	Match(v any) (bool, error)
}

type FilterArgs struct {
	ctx context.Context
	v   any
}

func (a *FilterArgs) Context() context.Context {
	return a.ctx
}

func (a *FilterArgs) Value() any {
	return a.v
}

type FilterFunc func(v any) (bool, error)

func (f FilterFunc) Match(v any) (bool, error) { return f(v) }

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}
