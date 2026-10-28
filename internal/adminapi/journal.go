package adminapi

import (
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/verify"
)

var _ Journal = (*journal.Journal)(nil)

// Journal is the journal of the requests a mock server serves, such as a *journal.Journal.
type Journal interface {
	verify.Journal
	Last() uint64
	Get(id string) (*journal.Entry, error)
	Clear() error
}

// RequestPattern makes the pattern of the request part of a mock of the server's protocol, v, as a mock file writes
// it: the request key of a query.
type RequestPattern func(v config.Value) (verify.Pattern, error)
