package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// requestID is the input of get_request.
type requestID struct {
	ID string `json:"id" jsonschema:"The id of the request, as the VLatte-Request-Id header of its answer names it."`
}

func addGetRequestTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "get_request",
		Description: "Get a request of the journal, with its answer and the mock that answered it.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(_ context.Context, in requestID) (any, error) { return ops.GetRequest(in.ID) })
}
