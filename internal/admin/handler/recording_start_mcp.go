package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordingSettings is the input of start_recording.
type recordingSettings struct {
	Settings jsontext.Value `json:"settings,omitempty" jsonschema:"The keys of the record section of the server's settings, such as target and paths. None keeps the server's."`
}

func addStartRecordingTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "start_recording",
		Description: "Start a recording: the requests it takes go to its target, and their answers become mock files " +
			"once stop_recording stops it.",
	}

	addTool(s, tool, func(_ context.Context, in recordingSettings) (any, error) {
		return ops.StartRecording(in.Settings, _formatJSON)
	})
}
