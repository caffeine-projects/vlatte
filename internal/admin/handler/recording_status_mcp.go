package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addRecordingStatusTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "recording_status",
		Description: "Count the calls of the recording, and the mocks it wrote, or tell that none runs.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return ops.RecordingStatus() })
}
