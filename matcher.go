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

// MatcherArg is the argument a [Matcher] gets: the value to match, with the context and the explanation of the match.
type MatcherArg struct {
	ctx  context.Context
	exp  *Explanation
	v    any
	mock Mock
}

// NewMatcherArg creates a [MatcherArg] for v, such as for a matcher that hands part of its value to another. exp,
// which records why the value matched or not, can be nil.
func NewMatcherArg(ctx context.Context, exp *Explanation, v any) MatcherArg {
	return MatcherArg{ctx: ctx, exp: exp, v: v}
}

// Context returns the context of the match, such as that of the request.
func (a *MatcherArg) Context() context.Context {
	return a.ctx
}

// Explain returns the optional explain block for recording [Matcher] messages.
// E.g. why a [Matcher] did not match).
func (a *MatcherArg) Explain() *Explanation {
	return a.exp
}

// Value returns the value to match, such as the part of the request the Selector of an [Eval] selected.
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

// MatcherFunc returns a [Matcher] named name that matches with fn.
func MatcherFunc(name string, fn func(arg MatcherArg) (bool, error)) Matcher {
	return &fnDelegate{name: name, fn: fn}
}
