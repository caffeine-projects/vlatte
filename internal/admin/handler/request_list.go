package handler

import (
	"context"
	"strconv"

	"github.com/caffeine-projects/vlatte/journal"
)

// ListRequests returns the newest requests the server served: a page of limit of them, of the requests before before,
// the next of a page, or the newest for 0. A limit of 0 returns their count only; one outside 0 to 1000 is an
// invalid_parameter problem. It needs a server that keeps a journal.
func (o *Operations) ListRequests(ctx context.Context, limit int, before uint64) (RequestList, error) {
	if !pageLimit(int64(limit)) {
		return RequestList{}, invalidParameter("limit", strconv.Itoa(limit), _limitRule)
	}

	return o.findRequests(ctx, journal.Query{Before: before}, nil, limit)
}
