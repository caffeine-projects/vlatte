package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// requestQueryInput is the input of query_requests.
type requestQueryInput struct {
	Query map[string]any `json:"query" jsonschema:"The query, as QUERY /requests of the admin API takes it: request, a mock file's request part, outcome, mock_id, since, until, limit and before."`
}

func addQueryRequestsTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "query_requests",
		Description: "Find and count the requests of the journal a query selects, such as those a mock's request part matches.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in requestQueryInput) (any, error) {
		doc, err := document(in.Query)
		if err != nil {
			return nil, err
		}

		return ops.QueryRequests(ctx, doc, _formatJSON)
	})
}
