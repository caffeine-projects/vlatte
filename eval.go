package vlatte

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

var _ fmt.Stringer = (*Eval[any])(nil)

var (
	ErrMatchPanicked = errors.New("mock: panic")
	ErrMatching      = errors.New("mock: matching")
)

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
	Matcher Matcher

	// Selector will extract the [http.Request] or a specific field of it and feed it to the associated [Matcher].
	Selector Selector[TIn]
}

// String returns a simple string representation of this [Eval].
func (e *Eval[TIn]) String() string {
	return fmt.Sprintf("%v(%s)", e.Target, e.Key)
}

// Match evaluates the input against the matcher of this [Eval].
func (e *Eval[TIn]) Match(ctx context.Context, sub *Explanation, in TIn) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: Match: type: %T %v", ErrMatchPanicked, e.Matcher, r)
		}
	}()

	return e.Matcher.Match(e.Arg(ctx, sub, in))
}

// Arg returns the argument the matcher of e gets for in: the value its Selector selects, or nil without one, with an
// explanation under sub. A mock server that reads a matcher of the mock Find picked, such as a scenario's to journal
// its move, gives it the same argument.
func (e *Eval[TIn]) Arg(ctx context.Context, sub *Explanation, in TIn) MatcherArg {
	var val any
	if e.Selector != nil {
		val = e.Selector(ctx, in)
	}

	return NewMatcherArg(ctx, sub.Sub(e.Matcher.Name()), val)
}

// GetEvals is a function that returns a list of evaluators of a given [Mock].
// Normally [Mock] will have a function that returns a list of evaluators.
type GetEvals[M Mock, TIn any] func(m M) Evaluators[M, TIn]

// Mocks is a list of sorted eligible mocks.
// This list can searched for a match.
type Mocks[M Mock, TIn any] iter.Seq[M]

// Find tries to find an exact match in the mocks.
// It returns the mock that matched and a boolean indicating if a match was found.
// misses, when not nil, starts over and keeps each mock that didn't match, with the part it stopped at, which
// [Misses.NearMisses] goes on from.
//
// The mock found has taken the turn of its [Claimer] matcher. When another request took it first, Find looks again from
// the first mock, as the mocks now match in the state that request left. A retry adds to sub. Find stops with the
// error of ctx once it ends.
func (mocks Mocks[M, TIn]) Find(
	ctx context.Context,
	sub *Explanation,
	in TIn,
	evals GetEvals[M, TIn],
	misses *Misses[M, TIn],
) (M, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			var nothing M
			return nothing, false, err
		}

		mock, found, claimed, err := mocks.find(ctx, sub, in, evals, misses)
		if err != nil || !found || claimed {
			return mock, found, err
		}
	}
}

// find is one search of [Mocks.Find]. It reports whether the mock it found took the turns of its [Claimer] matchers.
func (mocks Mocks[M, TIn]) find(
	ctx context.Context,
	sub *Explanation,
	in TIn,
	evals GetEvals[M, TIn],
	misses *Misses[M, TIn],
) (M, bool, bool, error) {
	if misses != nil {
		misses.in, misses.evals, misses.tried = in, evals, misses.tried[:0]
	}

	for mock := range mocks {
		e := evals(mock)

		stop, err := e.stop(ctx, sub, in)
		if err != nil {
			var nothing M
			return nothing, false, false, fmt.Errorf("%w: %s: %w", ErrMatching, mock.String(), err)
		}

		if stop == len(e) {
			if misses != nil {
				misses.tried = misses.tried[:0]
			}

			claimed, err := e.claim(ctx, in)
			if err != nil {
				var nothing M
				return nothing, false, false, fmt.Errorf("%w: %s: %w", ErrMatching, mock.String(), err)
			}

			return mock, true, claimed, nil
		}

		if misses != nil {
			misses.tried = append(misses.tried, tried[M]{mock: mock, stop: stop})
		}
	}

	var nothing M
	return nothing, false, false, nil
}

// Evaluators is a list of evaluators.
type Evaluators[M Mock, TIn any] []*Eval[TIn]

// Eval evaluates the input against all evaluators from a single [Mock].
// Normally this function is used together with [Mocks.Find]
func (e Evaluators[M, TIn]) Eval(ctx context.Context, sub *Explanation, in TIn) (bool, error) {
	stop, err := e.stop(ctx, sub, in)
	return err == nil && stop == len(e), err
}

// claim takes the turns of the [Claimer] matchers for in, in order, up to the first another request took: it reports
// whether it took them all.
func (e Evaluators[M, TIn]) claim(ctx context.Context, in TIn) (claimed bool, err error) {
	for i, eval := range e {
		c, ok := eval.Matcher.(Claimer)
		if !ok {
			continue
		}

		if claimed, err = claimOne(ctx, c, eval, in); err != nil || !claimed {
			if err != nil {
				err = fmt.Errorf("eval[%d][%s]: %w", i, eval.String(), err)
			}

			return claimed, err
		}
	}

	return true, nil
}

// claimOne takes the turn of c, the matcher of eval, for in, turning a panic into an error, as [Eval.Match] does.
func claimOne[TIn any](ctx context.Context, c Claimer, eval *Eval[TIn], in TIn) (claimed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: Claim: type: %T %v", ErrMatchPanicked, c, r)
		}
	}()

	return c.Claim(eval.Arg(ctx, nil, in)), nil
}

// stop evaluates the input against the evaluators in order, up to the first that doesn't match: it returns its
// index, or len(e) when all match.
func (e Evaluators[M, TIn]) stop(ctx context.Context, sub *Explanation, in TIn) (int, error) {
	for i, eval := range e {
		ok, err := eval.Match(ctx, sub, in)
		if err != nil {
			return i, fmt.Errorf("eval[%d][%s]: %w", i, eval.String(), err)
		}

		if !ok {
			return i, nil
		}
	}

	return len(e), nil
}
