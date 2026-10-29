package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// requestPage is the input of list_requests.
type requestPage struct {
	Limit  *int   `json:"limit,omitempty" jsonschema:"How many requests to list, from 0 to 1000: 0 counts them only. None lists 100."`
	Before uint64 `json:"before,omitempty" jsonschema:"The next of a page, to list the requests before it. None lists the newest."`
	After  uint64 `json:"after,omitempty" jsonschema:"The mark of a list, to list the requests after it only. None lists them all."`
}

func addListRequestsTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "list_requests",
		Description: "List the newest requests of the journal, newest first, with their count, and the next of the page.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in requestPage) (any, error) {
		limit := _requestsPage
		if in.Limit != nil {
			limit = *in.Limit
		}

		return ops.ListRequests(ctx, limit, in.Before, in.After)
	})
}
