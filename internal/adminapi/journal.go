package adminapi

import (
	"context"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

var _ Journal = (*journal.Journal)(nil)

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
