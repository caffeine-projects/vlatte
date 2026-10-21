package vlatte

import (
	"cmp"
	"context"
	"slices"
)

// _nearMisses is how many mocks a near miss report names at most.
const _nearMisses = 3

// NearMisses returns the mocks that came closest to matching in, a request none matched: at most 3, those with the
// fewest parts that failed first, then those with the most that passed, in the order of mocks. A mock none of whose
// parts passed is left out, unless it has only parts it skipped. describe names the part of the request an
// evaluator checks, such as "header Accept"; a part whose matcher failed with an error names it too.
//
// It runs every matcher of each mock again, not only up to the first that fails, so the action of a matcher such as
// match.Peek runs again too. It skips those that keep state, as running a scenario's may change it: their parts,
// such as Times or a scenario's state, are those a mock all of whose other parts passed missed by.
func (mocks Mocks[M, TIn]) NearMisses(ctx context.Context, in TIn, evals GetEvals[M, TIn], describe func(*Eval[TIn]) string) []NearMiss {
	var misses []NearMiss

	for mock := range mocks {
		if ctx.Err() != nil {
			break
		}

		miss := NearMiss{MockID: mock.ID(), Name: mock.Name()}
		exp := NewExplanation()

		for _, eval := range evals(mock) {
			if _, ok := eval.Matcher.(Stateful); ok {
				miss.Skipped = append(miss.Skipped, describe(eval))
				continue
			}

			miss.Total++

			switch ok, err := eval.Match(ctx, exp, in); {
			case err != nil:
				miss.Failed = append(miss.Failed, describe(eval)+": "+err.Error())
			case ok:
				miss.Passed++
			default:
				miss.Failed = append(miss.Failed, describe(eval))
			}
		}

		if miss.Passed > 0 || (len(miss.Failed) == 0 && len(miss.Skipped) > 0) {
			misses = append(misses, miss)
		}
	}

	slices.SortStableFunc(misses, func(a, b NearMiss) int {
		return cmp.Or(cmp.Compare(len(a.Failed), len(b.Failed)), cmp.Compare(b.Passed, a.Passed))
	})

	return misses[:min(len(misses), _nearMisses)]
}
