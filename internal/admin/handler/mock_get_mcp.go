package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockID is the input of a tool on one mock.
type mockID struct {
	ID string `json:"id" jsonschema:"The id of the mock."`
}

func addGetMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "get_mock",
		Description: "Get a mock as a mock file describes it, which replace_mock takes back.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in mockID) (any, error) {
		desc, err := ops.GetMock(ctx, in.ID)
		if err != nil {
			return nil, err
		}

		body, err := encode(desc, _formatJSON)
		if err != nil {
			return nil, describeFailed(in.ID, err)
		}

		return jsontext.Value(body), nil
	})
}
