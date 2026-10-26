package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addValidateMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "validate_mock",
		Description: "Check a mock as add_mock would, without adding it, and list its problems.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in mockDocument) (any, error) {
		doc, err := document(in.Mock)
		if err != nil {
			return nil, err
		}

		return ops.ValidateMock(ctx, doc, _formatJSON)
	})
}
