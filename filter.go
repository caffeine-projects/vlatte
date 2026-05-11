package vlatte

import (
	"context"

	"github.com/caffeine-projects/vlatte/vlattecore/explain"
)

var _ Filter = (*fnDelegate)(nil)

// Filter matches values.
type Filter interface {
	Name() string
	Match(arg FilterArg) (bool, error)
}

type FilterArg struct {
	ctx context.Context
	exp *explain.Block
	v   any
}

func NewFilterArg(ctx context.Context, exp *explain.Block, v any) FilterArg {
	return FilterArg{ctx: ctx, exp: exp, v: v}
}

func (a *FilterArg) Context() context.Context {
	return a.ctx
}

// Explain returns the optional explain block for recording [Filter] messages.
// E.g. why a [Filter] did not match).
func (a *FilterArg) Explain() *explain.Block {
	return a.exp
}

func (a *FilterArg) Value() any {
	return a.v
}

type fnDelegate struct {
	name string
	fn   func(FilterArg) (bool, error)
}

func (f *fnDelegate) Name() string                      { return f.name }
func (f *fnDelegate) Match(arg FilterArg) (bool, error) { return f.fn(arg) }

func FilterFunc(name string, fn func(arg FilterArg) (bool, error)) Filter {
	return &fnDelegate{name: name, fn: fn}
}
