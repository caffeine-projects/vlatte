package handler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

// _maxWait is the longest a check of QUERY /requests/verify waits: its client holds a connection as long.
const _maxWait = 30 * time.Second

var (
	_requestStepSchema = config.NewSchema(selectionFields()...)

	_requestCheckSchema = config.NewSchema(append(selectionFields(),
		config.Field[requestQuery]{
			Key: "after", Kind: config.Int, Usage: "Check the requests after this one, the mark of a list.",
			Set: func(c *requestQuery, v config.Value) error {
				c.marked = true
				return setAfter(&c.q, v)
			},
		},
		config.Field[requestQuery]{
			Key: "count", Kind: config.Any, Usage: "The number of requests: a number, or a matcher list such as [\"gte\", 2]. None is at least 1.",
			Set: func(c *requestQuery, v config.Value) error {
				m, err := matcherOf(c.matchers, v)
				if err != nil {
					return err
				}

				c.count, c.expect = m, "a number matching "+m.Name()+" of"
				if _, ok := v.Any.(int64); ok {
					c.expect = fmt.Sprintf("exactly %d", v.Any)
				}

				return nil
			},
		},
		waitField("within", "Wait up to this long for the count to pass, such as 2s.",
			func(c *requestQuery, d time.Duration) { c.within = d }),
		waitField("during", "Wait this long for the count to fail, such as 2s: it passes if it doesn't.",
			func(c *requestQuery, d time.Duration) { c.during = d }),
		config.Field[requestQuery]{
			Key: "in_order", Kind: config.Any, Usage: "The steps of an order, each the keys of a query that select its requests.",
			Set: func(c *requestQuery, v config.Value) error {
				return config.Items(v, func(item config.Value) error {
					step := requestQuery{matchers: c.matchers}
					err := _requestStepSchema.Decode(&step, item)
					c.steps = append(c.steps, step)

					return err
				})
			},
		},
	)...)
)

// RequestVerdict is the answer of [Operations.VerifyRequests], QUERY /requests/verify: whether the check passed, how
// many requests it selected, an order none, and how long it waited. A check that failed lists the requests that tell
// why, as the report of a Go test's check does: those selected, and, when it wanted more, those that came closest.
type RequestVerdict struct {
	Passed   bool            `json:"passed"`
	Count    *int            `json:"count,omitempty"`
	WaitedMS int64           `json:"waited_ms"`
	Selected []verify.Listed `json:"selected,omitempty"`
	Closest  []verify.Listed `json:"closest,omitempty"`
	Report   string          `json:"report,omitempty"`
}

// VerifyRequests checks the requests of the journal the check of doc, a document in format, describes, as a Go test
// checks them with package verify: their count, at least 1 for none, now or over a wait of within or during, or their
// order, in_order. A check that fails is an answer, not an error. A check that can't count, as when the journal
// dropped requests it counts, is an incomplete_journal problem. A wait ends with the admin API, as a closing problem.
// It needs a server that keeps a journal.
func (o *Operations) VerifyRequests(ctx context.Context, doc []byte, format string) (RequestVerdict, error) {
	if o.journal == nil {
		return RequestVerdict{}, notSupported(_lackingJournal)
	}

	check := requestQuery{matchers: o.matchers, count: match.GTE(1), expect: "at least 1"}

	keys, err := loadBody(_requestCheckSchema, &check, doc, format)
	if err != nil {
		return RequestVerdict{}, invalid("the check", err)
	}

	if _, ordered := keys["in_order"]; ordered {
		return o.verifyOrder(ctx, check, keys)
	}

	spec, err := o.specOf(check)
	if err != nil {
		return RequestVerdict{}, invalid("the check", err)
	}

	waiting, stop := o.waiting(ctx)
	defer stop()

	result, err := verify.Check(waiting, spec)
	if err != nil {
		return RequestVerdict{}, checkError(ctx, err)
	}

	return RequestVerdict{
		Passed: result.Passed, Count: &result.Count, WaitedMS: result.Waited.Milliseconds(),
		Selected: result.Selected, Closest: result.Closest, Report: result.Report,
	}, nil
}

// verifyOrder checks the order of the steps of check, whose keys are those its document set.
func (o *Operations) verifyOrder(ctx context.Context, check requestQuery, keys map[string]config.Value) (RequestVerdict, error) {
	extra := slices.DeleteFunc(slices.Sorted(maps.Keys(keys)), func(k string) bool { return k == "in_order" || k == "after" })
	if len(extra) > 0 {
		return RequestVerdict{}, invalid("the check", fmt.Errorf("in_order takes the place of %s: each step has its own", strings.Join(extra, ", ")))
	}

	specs := make([]verify.Spec, len(check.steps))

	for i, step := range check.steps {
		step.q.After, step.marked = check.q.After, check.marked

		spec, err := o.specOf(step)
		if err != nil {
			return RequestVerdict{}, invalid("the check", fmt.Errorf("in_order[%d]: %w", i, err))
		}

		specs[i] = spec
	}

	result, err := verify.CheckOrder(ctx, specs...)
	if err != nil {
		return RequestVerdict{}, checkError(ctx, err)
	}

	return RequestVerdict{Passed: result.Passed, Report: result.Report}, nil
}

// specOf returns the spec of the check of c, a check of the journal by its keys.
func (o *Operations) specOf(c requestQuery) (verify.Spec, error) {
	spec := verify.Spec{
		Journal: o.journal, Query: c.q, Marked: c.marked,
		Count: c.count, Expect: c.expect, Within: c.within, During: c.during,
	}

	if c.request != nil {
		pattern, err := requestPattern(*c.request, o.pattern)
		if err != nil {
			return verify.Spec{}, err
		}

		spec.Patterns = []verify.Pattern{pattern}
	}

	return spec, nil
}

// checkError returns the problem of err, the error of a check of ctx, the caller's context.
func checkError(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		// The caller left: the check didn't fail, and isn't the caller's problem.
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, journal.ErrClosed):
		// The caller is there: the admin API ended the wait, or the mock server closed its journal.
		return &problemError{status: http.StatusServiceUnavailable, code: _codeClosing, detail: "the mock server is closing"}
	case errors.Is(err, verify.ErrCleared), errors.Is(err, verify.ErrDropped):
		return &problemError{status: http.StatusConflict, code: _codeIncompleteJournal, detail: err.Error()}
	case errors.Is(err, verify.ErrInvalidSpec), errors.Is(err, vlatte.ErrMatching), errors.Is(err, vlatte.ErrMatchPanicked):
		return &problemError{status: http.StatusBadRequest, code: _codeInvalidBody, detail: "the check: " + err.Error()}
	}

	return err
}

// waitField declares a key of a check whose value is a wait, up to _maxWait.
func waitField(key, usage string, set func(c *requestQuery, d time.Duration)) config.Field[requestQuery] {
	return config.Field[requestQuery]{
		Key: key, Kind: config.Duration, Usage: usage,
		Set: func(c *requestQuery, v config.Value) error {
			if v.Dur <= 0 || v.Dur > _maxWait {
				return config.Invalid(v, fmt.Sprintf("%s must be longer than 0 and at most %s", key, _maxWait), "check again for a longer wait")
			}

			set(c, v.Dur)

			return nil
		},
	}
}
