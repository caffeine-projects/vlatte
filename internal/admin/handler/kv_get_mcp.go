package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// kvKey is the input of a tool on one key of the key-value store.
type kvKey struct {
	Key string `json:"key" jsonschema:"The key."`
}

func addGetKVTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "get_kv",
		Description: "Get the value of a key of the key-value store, which templates read as .KV.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, in kvKey) (any, error) { return ops.GetKV(ctx, in.Key) })
}
