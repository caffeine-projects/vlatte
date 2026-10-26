package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// saveInput is the input of save_mocks.
type saveInput struct {
	Dir    string `json:"dir,omitempty" jsonschema:"Directory to write the mock files into, inside the server's root directory. Empty is the server's default."`
	Format string `json:"format,omitempty" jsonschema:"Format of the mock files: json, yaml or yml. Empty is yaml."`
}

func addSaveMocksTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "save_mocks",
		Description: "Write the mocks this API added as mock files, <id>.mock.<format>, which the server serves from then " +
			"on, and list their paths. Nothing is written when a mock can't be written as a mock file that loads back.",
	}

	addTool(s, tool, func(ctx context.Context, in saveInput) (any, error) {
		settings := make(map[string]any)
		if in.Dir != "" {
			settings["dir"] = in.Dir
		}

		if in.Format != "" {
			settings["format"] = in.Format
		}

		doc, err := document(settings)
		if err != nil {
			return nil, err
		}

		return ops.SaveMocks(ctx, doc, _formatJSON)
	})
}
