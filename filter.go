package vlatte

import "context"

// Filter matches values.
type Filter interface {
	Match(arg FilterArg) (bool, error)
}

type FilterArg struct {
	ctx context.Context
	v   any
}

func (a *FilterArg) Context() context.Context {
	return a.ctx
}

func (a *FilterArg) Value() any {
	return a.v
}

func NewFilterArg(ctx context.Context, v any) FilterArg {
	return FilterArg{ctx: ctx, v: v}
}

type FilterFunc func(arg FilterArg) (bool, error)

func (f FilterFunc) Match(arg FilterArg) (bool, error) { return f(arg) }

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}
