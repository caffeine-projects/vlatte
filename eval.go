package vlatte

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

var _ fmt.Stringer = (*Eval[any])(nil)

var ErrMatchPanicked = errors.New("mock: panic")
var ErrMatching = errors.New("mock: matcher")

// Selector selects a value from the parameter input.
// The return value could be the input itself or a specific field of it.
type Selector[TIn any] func(ctx context.Context, input TIn) any

// Eval describes a matching evaluator.
// It contains all the necessary information to evaluate a match against something, eg.: the HTTP request or a piece of it.
// It also contains data to better describe mismatches.
type Eval[TIn any] struct {
	// Key describes to which specific part within the HTTP request parts a matcher is associated.
	// Eg.: a matcher for Header(Content-Type), the key would be "Content-Type".
	// A matcher can be associated to, for example the whole Header.
	// In this case, the key will be empty even if the matcher targets just one header name.
	Key string

	// Target is the index of the target of this evaluator.
	// Eg.: If this evaluator is targeting the header, the target should specify it, regardeless of which header name.
	// So, every evaluator targeting the header should have the same target value.
	Target int

	// Matcher associated with this [Eval].
	Matcher Filter

	// Selector will extract the [http.Request] or a specific field of it and feed it to the associated [filter.Matcher].
	Selector Selector[TIn]
}

// String returns a simple string representation of this [Eval].
func (e *Eval[TIn]) String() string {
	return fmt.Sprintf("%v(%s)", e.Target, e.Key)
}

// Match evaluates the input against the matcher of this [Eval].
func (e *Eval[TIn]) Match(ctx context.Context, in TIn) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: Match: type: %T %v", ErrMatchPanicked, e.Matcher, r)
		}
	}()

	var val any
	if e.Selector != nil {
		val = e.Selector(ctx, in)
	}

	return e.Matcher.Match(NewFilterArg(ctx, val))
}

// GetEvals is a function that returns a list of evaluators of a given [Bean].
// Normally [Bean] will have a function that returns a list of evaluators.
type GetEvals[M Bean, TIn any] func(m M) Evaluators[M, TIn]

// Mocks is a list of sorted eligible mocks.
// This list can searched for a match.
type Mocks[M Bean, TIn any] iter.Seq[M]

// Find tries to find an exact match in the mocks.
func (mocks Mocks[M, TIn]) Find(ctx context.Context, in TIn, evals GetEvals[M, TIn]) (M, bool, error) {
	i := 0
	for mock := range mocks {
		ok, err := evals(mock).Eval(ctx, in)
		if err != nil {
			var nothing M
			return nothing, false, fmt.Errorf("%w: error at mock[%d] %s. reason: %w", ErrMatching, i, mock.String(), err)
		}

		if ok {
			return mock, true, nil
		}

		i++
	}

	var nothing M
	return nothing, false, nil
}

// Evaluators is a list of evaluators.
type Evaluators[M Bean, TIn any] []*Eval[TIn]

// Eval evaluates the input against all evaluators from a single [Bean].
// Normally this function is used together with [Mocks.Find]
func (e Evaluators[M, TIn]) Eval(ctx context.Context, in TIn) (bool, error) {
	for i, eval := range e {
		ok, err := eval.Match(ctx, in)
		if err != nil {
			return false, fmt.Errorf("eval[%d]: %s. reason: %w", i, eval.String(), err)
		}

		if !ok {
			return false, nil
		}
	}

	return true, nil
}
