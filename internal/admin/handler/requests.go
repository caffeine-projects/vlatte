package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

var _ Journal = (*journal.Journal)(nil)

const (
	// _requestsPage is how many requests a list answers when it names no limit, and _maxRequestsPage the most it
	// answers: a page holds whole bodies.
	_requestsPage    = 100
	_maxRequestsPage = 1000
)

// Journal is the journal of the requests a mock server serves, such as a *journal.Journal.
type Journal interface {
	Find(ctx context.Context, q journal.Query, filters ...journal.Filter) (journal.Page, error)
	Count(ctx context.Context, q journal.Query, filters ...journal.Filter) (int, error)
	Get(id string) (*journal.Entry, error)
	Clear() error
}

// RequestPattern makes the filter of the request part of a mock of the server's protocol, v, as a mock file writes
// it: the request key of a query.
type RequestPattern func(v config.Value) (journal.Filter, error)

// RequestList is the answer of [Operations.ListRequests] and [Operations.QueryRequests], GET and QUERY /requests: count
// is how many requests the query selects, whatever page requests holds, and next, the before of the next page, when
// there is one.
type RequestList struct {
	Count    int              `json:"count"`
	Requests []*journal.Entry `json:"requests"`
	Next     uint64           `json:"next,omitzero"`
}

// findRequests returns the requests q, with filters, selects, a page of limit of them, or their count only for 0. A
// matcher of the query that fails, or panics, is the query's problem.
func (o *Operations) findRequests(ctx context.Context, q journal.Query, filters []journal.Filter, limit int) (RequestList, error) {
	if o.journal == nil {
		return RequestList{}, notSupported(_lackingJournal)
	}

	var (
		list RequestList
		err  error
	)

	if limit == 0 {
		list.Count, err = o.journal.Count(ctx, q, filters...)
	} else {
		q.Limit = limit

		var page journal.Page
		if page, err = o.journal.Find(ctx, q, filters...); err == nil {
			list = RequestList{Count: page.Count, Requests: page.Entries, Next: page.Next}
		}
	}

	switch {
	case err == nil:
	case ctx.Err() != nil:
		// The caller left: the query didn't fail, and isn't the caller's problem.
		return RequestList{}, err
	case errors.Is(err, vlatte.ErrMatching), errors.Is(err, vlatte.ErrMatchPanicked):
		return RequestList{}, &problemError{status: http.StatusBadRequest, code: _codeInvalidBody, detail: "the query: " + err.Error()}
	default:
		return RequestList{}, err
	}

	// An empty page is a list, not null.
	if list.Requests == nil {
		list.Requests = []*journal.Entry{}
	}

	return list, nil
}

// _limitRule says what pageLimit takes.
var _limitRule = fmt.Sprintf("a number from 0 to %d", _maxRequestsPage)

// pageLimit tells whether n is a limit of a page of requests.
func pageLimit(n int64) bool {
	return n >= 0 && n <= _maxRequestsPage
}

func checkLimit(v config.Value) error {
	if !pageLimit(v.Int) {
		return config.Invalid(v, "limit must be "+_limitRule, "page with before")
	}

	return nil
}

// invalidParameter is the problem of a parameter, name, whose value isn't want.
func invalidParameter(name, value, want string) *problemError {
	return &problemError{
		status: http.StatusBadRequest, code: _codeInvalidParameter,
		detail: fmt.Sprintf("the parameter %s=%q must be %s", name, value, want),
	}
}
