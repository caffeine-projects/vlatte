package vlatte

import (
	"context"
)

var _ Matcher = (*fnDelegate)(nil)

// Matcher matches values.
type Matcher interface {
	Name() string
	Match(arg MatcherArg) (bool, error)
}

type MatcherArg struct {
	ctx  context.Context
	exp  *Explanation
	v    any
	mock Mock
}

func NewMatcherArg(ctx context.Context, exp *Explanation, v any) MatcherArg {
	return MatcherArg{ctx: ctx, exp: exp, v: v}
}

func (a *MatcherArg) Context() context.Context {
	return a.ctx
}

// Explain returns the optional explain block for recording [Matcher] messages.
// E.g. why a [Matcher] did not match).
func (a *MatcherArg) Explain() *Explanation {
	return a.exp
}

func (a *MatcherArg) Value() any {
	return a.v
}

// Mock returns the mock whose turn a [Claimer] takes, in the argument of its Claim. It is nil in the argument of
// Match, which has no turn to take.
func (a *MatcherArg) Mock() Mock {
	return a.mock
}

type fnDelegate struct {
	name string
	fn   func(MatcherArg) (bool, error)
}

func (f *fnDelegate) Name() string                       { return f.name }
func (f *fnDelegate) Match(arg MatcherArg) (bool, error) { return f.fn(arg) }

func MatcherFunc(name string, fn func(arg MatcherArg) (bool, error)) Matcher {
	return &fnDelegate{name: name, fn: fn}
}
