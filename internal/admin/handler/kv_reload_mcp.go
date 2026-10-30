package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addReloadKVTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "reload_kv",
		Description: "Load the key-value store, which templates read as .KV, again from its file: values set since are " +
			"gone, and keys removed since come back. A file with a problem changes nothing.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return nil, ops.ReloadKV(ctx) })
}
