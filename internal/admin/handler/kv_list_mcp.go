package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addListKVTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "list_kv",
		Description: "List the keys of the key-value store, which templates read as .KV, without their values.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return ops.ListKV(ctx) })
}
