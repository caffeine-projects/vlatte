package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// kvValue is the input of set_kv.
type kvValue struct {
	Key   string `json:"key" jsonschema:"The key."`
	Value string `json:"value" jsonschema:"The value of the key, as text. An empty one is a value."`
}

func addSetKVTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "set_kv",
		Description: "Set a key of the key-value store, which templates read as .KV, to a value: the next request reads it.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in kvValue) (any, error) { return ops.setKV(ctx, in.Key, in.Value) })
}
