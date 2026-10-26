package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addStopRecordingTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "stop_recording",
		Description: "Stop the recording, and list the mock files it wrote, which the server serves from then on.",
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return ops.StopRecording(ctx) })
}
