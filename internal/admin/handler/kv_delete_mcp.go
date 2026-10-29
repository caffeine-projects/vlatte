package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addDeleteKVTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "delete_kv",
		Description: "Remove a key from the key-value store, which templates read as .KV. A key that isn't set is no error.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in kvKey) (any, error) { return nil, ops.DeleteKV(ctx, in.Key) })
}
