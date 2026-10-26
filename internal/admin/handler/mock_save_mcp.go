package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// saveInput is the input of save_mocks: the body of POST /mocks/save.
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

	addArgumentsTool[saveInput](s, tool, func(ctx context.Context, args jsontext.Value) (any, error) {
		return ops.SaveMocks(ctx, args, _formatJSON)
	})
}
