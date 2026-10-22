package vlatte

import (
	"cmp"
	"context"
	"slices"
)

// _nearMisses is how many mocks a near miss report names at most.
const _nearMisses = 3

// NearMiss is a mock that came close to matching a request: Passed of the Total parts it checked matched. Failed names
// the parts that didn't, and Skipped those it didn't check, which keep state, such as a scenario's, that matching
// never reached.
type NearMiss struct {
	MockID  string   `json:"mock_id"`
	Name    string   `json:"name,omitempty"`
	Passed  int      `json:"passed"`
	Total   int      `json:"total"`
	Failed  []string `json:"failed,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// Misses are the mocks [Mocks.Find] tried for a request, and that didn't match it, each with the part it stopped at:
// none once a mock matched. The zero value is ready to use. A server may reuse one, a request at a time, such as
// through a sync.Pool: Find starts it over, and Reset lets go of what it kept.
type Misses[M Mock, TIn any] struct {
	in    TIn
	evals GetEvals[M, TIn]
	tried []tried[M]
}

// tried is a mock Find tried: the parts before stop matched, and the one at stop didn't.
type tried[M Mock] struct {
	mock M
	stop int
}

// Reset lets go of the mocks and the request Find kept, keeping the room they took for the next request.
func (m *Misses[M, TIn]) Reset() {
	clear(m.tried[:cap(m.tried)])

	var in TIn
	m.in, m.evals, m.tried = in, nil, m.tried[:0]
}

// nearMiss is a NearMiss as NearMisses ranks it: differs counts the parts that failed and keep no state, the parts
// of the request.
type nearMiss struct {
	miss    NearMiss
	differs int
}

// NearMisses returns the mocks that came closest to matching the request Find found no mock for: at most 3, those
// that differ in the fewest parts of the request first, then those with the most parts that passed, in the order
// Find tried them. A part that keeps state, such as a Times used up, is no part of the request: a mock that missed
// by it alone comes first. describe names the part of the request an evaluator checks, such as "header Accept"; a
// part whose matcher failed with an error names it too.
//
// No matcher runs twice for a request: each mock goes on from the part Find stopped at, which failed, and only the
// parts after it run now. Of those, it skips the ones that keep state, as running a scenario's may change it. A mock
// none of whose request parts passed is left out, unless it differs in none.
func (m *Misses[M, TIn]) NearMisses(ctx context.Context, describe func(*Eval[TIn]) string) []NearMiss {
	var ranked []nearMiss

	for _, t := range m.tried {
		if ctx.Err() != nil {
			break
		}

		near := nearMiss{miss: NearMiss{MockID: t.mock.ID(), Name: t.mock.Name()}}
		matched := 0

		for i, eval := range m.evals(t.mock) {
			_, keepsState := eval.Matcher.(Stateful)

			var (
				ok  bool
				err error
			)

			// Matching ran the parts up to the stop: those before it matched, and that one didn't.
			switch {
			case i < t.stop:
				ok = true
			case i == t.stop:
			case keepsState:
				near.miss.Skipped = append(near.miss.Skipped, describe(eval))
				continue
			default:
				ok, err = eval.Match(ctx, nil, m.in)
			}

			near.miss.Total++

			switch {
			case err != nil:
				near.miss.Failed = append(near.miss.Failed, describe(eval)+": "+err.Error())
			case ok:
				near.miss.Passed++
			default:
				near.miss.Failed = append(near.miss.Failed, describe(eval))
			}

			switch {
			case keepsState:
			case ok && err == nil:
				matched++
			default:
				near.differs++
			}
		}

		if matched > 0 || near.differs == 0 {
			ranked = append(ranked, near)
		}
	}

	slices.SortStableFunc(ranked, func(a, b nearMiss) int {
		return cmp.Or(cmp.Compare(a.differs, b.differs), cmp.Compare(b.miss.Passed, a.miss.Passed))
	})

	misses := make([]NearMiss, 0, min(len(ranked), _nearMisses))
	for _, near := range ranked[:min(len(ranked), _nearMisses)] {
		misses = append(misses, near.miss)
	}

	return misses
}
