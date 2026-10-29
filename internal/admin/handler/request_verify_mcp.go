package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// requestCheckInput is the input of verify_requests.
type requestCheckInput struct {
	Check jsontext.Value `json:"check" jsonschema:"The check, as QUERY /requests/verify of the admin API takes it: the keys of query_requests but limit and before, with count, a number or a matcher list, and within or during, a wait such as 2s; or in_order, a list of queries, in place of the keys and count."`
}

func addVerifyRequestsTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "verify_requests",
		Description: "Check that the journal holds the requests a test expects: how many, by when, or in what order. A failed check says why, with the requests that came closest.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in requestCheckInput) (any, error) {
		return ops.VerifyRequests(ctx, in.Check, _formatJSON)
	})
}
