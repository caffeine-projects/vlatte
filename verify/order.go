package verify

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
)

// InOrder asserts that the server got a request of each step, each after the request of the step before: login, then
// an order, then logout. Other requests may come between. It checks now: steps that wait, with Within or During, fail
// it, so wait for the last step with its own check first. Steps of different servers fail it too, as their journals
// don't share an order. On a failure, it reports with t.Errorf the step that had no request after the one before, and
// returns false.
func InOrder[TM vlatte.Mock, A any](t vlatte.TestingT, steps ...Requests[TM, A]) bool {
	t.Helper()

	result, err := inOrder(steps)
	switch {
	case err != nil:
		t.Errorf("\nCould not verify the order of the requests.\n%v", err)
		return false
	case !result.Passed:
		t.Errorf("%s", result.Report)
	}

	return result.Passed
}

// inOrder checks the order of steps.
func inOrder[TM vlatte.Mock, A any](steps []Requests[TM, A]) (Result, error) {
	specs := make([]Spec, len(steps))

	for i, step := range steps {
		s, err := step.spec(nil, "")
		if err != nil {
			return Result{}, fmt.Errorf("verify: step %d: %w", i+1, err)
		}

		specs[i] = s
	}

	return CheckOrder(context.Background(), specs...)
}

// CheckOrder checks that the journal holds a request of each of steps, each after the request of the step before,
// now: each step's Count, Within and During count for nothing, and steps that wait fail it, as do steps of different
// journals. Result's Report says, of each step, the request that came after the one before, or that none did. It
// fails with an error rather than check part of the requests, as [Check] does.
func CheckOrder(ctx context.Context, steps ...Spec) (Result, error) {
	if len(steps) == 0 {
		return Result{}, fmt.Errorf("%w: an order needs a step", ErrInvalidSpec)
	}

	for i := range steps {
		s := &steps[i]

		if err := s.checkSelection(); err != nil {
			return Result{}, fmt.Errorf("verify: step %d: %w", i+1, err)
		}

		switch {
		case s.Within != 0 || s.During != 0:
			return Result{}, fmt.Errorf("%w: step %d waits: an order is checked now, so wait for the last step first", ErrInvalidSpec, i+1)
		case s.Journal != steps[0].Journal:
			return Result{}, fmt.Errorf("%w: step %d is of another server's journal: their orders don't compare", ErrInvalidSpec, i+1)
		}
	}

	// Each step's request comes after the previous step's: the first of its requests after it.
	seqs := make([][]uint64, len(steps))

	for i := range steps {
		s := &steps[i]

		cleared := s.Journal.Cleared()

		page, err := s.Journal.Find(ctx, s.Query, s.filters()...)
		if err != nil {
			return Result{}, fmt.Errorf("verify: step %d: %w", i+1, err)
		}

		if err := s.whole(cleared); err != nil {
			return Result{}, fmt.Errorf("verify: step %d: %w", i+1, err)
		}

		for _, e := range page.Entries {
			seqs[i] = append(seqs[i], e.Seq)
		}

		slices.Sort(seqs[i])
	}

	var (
		b      strings.Builder
		prev   uint64
		failed bool
	)

	b.WriteString("\nExpected requests in this order:\n")

	for i := range steps {
		fmt.Fprintf(&b, "  %d. request%s: ", i+1, steps[i].describe())

		j, _ := slices.BinarySearch(seqs[i], prev+1)

		switch {
		case failed:
			b.WriteString("not checked\n")
		case j < len(seqs[i]):
			prev = seqs[i][j]
			fmt.Fprintf(&b, "#%d\n", prev)
		case len(seqs[i]) == 0:
			b.WriteString("none\n")

			failed = true
		default:
			fmt.Fprintf(&b, "none after #%d, %s before it\n", prev, describeSeqs(seqs[i]))

			failed = true
		}
	}

	if !failed {
		return Result{Passed: true}, nil
	}

	return Result{Report: b.String()}, nil
}

// describeSeqs names the requests of seqs, such as "#1, #2", the last ones of many.
func describeSeqs(seqs []uint64) string {
	shown := seqs[max(0, len(seqs)-3):]

	names := make([]string, len(shown))
	for i, seq := range shown {
		names[i] = fmt.Sprintf("#%d", seq)
	}

	if len(shown) < len(seqs) {
		return fmt.Sprintf("%d, the last %s", len(seqs), strings.Join(names, ", "))
	}

	return strings.Join(names, ", ")
}
