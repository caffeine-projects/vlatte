package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordingSettings is the input of start_recording.
type recordingSettings struct {
	Settings map[string]any `json:"settings,omitempty" jsonschema:"The keys of the record section of the server's settings, such as target and paths. None keeps the server's."`
}

func addStartRecordingTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "start_recording",
		Description: "Start a recording: the requests it takes go to its target, and their answers become mock files " +
			"once stop_recording stops it.",
	}

	addTool(s, tool, func(_ context.Context, in recordingSettings) (any, error) {
		var doc []byte
		if in.Settings != nil {
			var err error
			if doc, err = document(in.Settings); err != nil {
				return nil, err
			}
		}

		return ops.StartRecording(doc, _formatJSON)
	})
}
