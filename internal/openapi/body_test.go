package openapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestGenerate_WritesABodyInItsMediaType(t *testing.T) {
	// A mock file's body is text as is, anything else as JSON, or bytes in base64: a value must reach the client in
	// the media type its Content-Type names. Outside JSON, the generator writes no format of its own: data it can't
	// write as text is better disabled than sent as JSON under another Content-Type.
	pet := map[string]any{"name": "Rex", "tags": []any{"a", "b"}, "note": "<&>"}
	base64 := map[string]any{"type": "string", "contentEncoding": "base64"}

	tests := []struct {
		name       string
		media      string
		schema     any
		value      any
		serialized bool
		body       any
		base64     string
		disabled   bool
		warning    string
	}{
		{name: "a JSON object", media: "application/json", value: pet, body: pet},
		{name: "a JSON list", media: "application/vnd.api+json", value: []any{1}, body: []any{1}},
		{name: "JSON text", media: "application/json", value: "rex", body: `"rex"`},
		{name: "a JSON number", media: "application/json; charset=utf-8", value: 1.5, body: "1.5"},
		{name: "a JSON null", media: "application/json", value: nil, body: "null"},
		{name: "JSON of a media type with parameters", media: "Application/JSON ; charset=utf-8", value: true, body: "true"},
		{name: "XML text", media: "application/xml", value: "<pet/>", body: "<pet/>"},
		{name: "an XML number", media: "application/xml", value: 7, body: "7"},
		{name: "CSV text", media: "text/csv", value: "a,b\n1,2\n", body: "a,b\n1,2\n"},
		{name: "a CSV boolean", media: "text/csv", value: true, body: "true"},
		{name: "plain text", media: "text/plain", value: 42, body: "42"},
		{name: "text not said to be base64", media: "application/octet-stream", value: "AAEC", body: "AAEC"},
		{
			name: "an object outside JSON", media: "application/xml", schema: map[string]any{"$ref": "#/$defs/Pet"}, value: pet, disabled: true,
			warning: "the body is data, not text: written disabled: give a string example",
		},
		{
			name: "a list outside JSON", media: "text/csv", value: []any{"a", "b"}, disabled: true,
			warning: "the body is data, not text: written disabled: give a string example",
		},
		{name: "base64 by its content encoding", media: "image/png", schema: base64, value: "iVBORw0K", base64: "iVBORw0K"},
		{name: "base64 by its format, through a reference", media: "application/octet-stream", schema: map[string]any{"$ref": "#/$defs/Bytes"}, value: "AAEC", base64: "AAEC"},
		{
			name: "text its schema says is base64", media: "image/png", schema: base64, value: "not base64!", body: "not base64!",
			warning: "the body isn't base64, as its schema says: written as text",
		},
		{name: "empty base64", media: "image/png", schema: base64, value: "", body: ""},
		{name: "text as sent, which reads as base64", media: "text/plain", schema: base64, value: "aGVsbG8=", serialized: true, body: "aGVsbG8="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mocks, warnings := generateWithWarnings(t, &openapi.Document{
				Defs: map[string]any{"Bytes": map[string]any{"type": "string", "format": "byte"}, "Pet": map[string]any{"type": "object"}},
				Operations: []openapi.Operation{{
					Method: "GET", Path: "/x",
					Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
						{MediaType: tt.media, Schema: tt.schema, Examples: []openapi.Example{{Value: tt.value, Serialized: tt.serialized}}},
					}}},
				}},
			})

			var ok []openapi.Mock
			for _, m := range mocks {
				if m.Status == "200" {
					ok = append(ok, m)
				}
			}

			require.Len(t, ok, 1)
			require.Equal(t, tt.body, ok[0].Reply.Body)
			require.Equal(t, tt.base64, ok[0].Reply.BodyBase64)
			require.Equal(t, !tt.disabled, ok[0].Enabled)

			if tt.disabled {
				require.NotContains(t, ok[0].Reply.Header, "Content-Type")
			} else {
				require.Equal(t, tt.media, ok[0].Reply.Header["Content-Type"])
			}

			if tt.warning == "" {
				require.Empty(t, warnings)
			} else {
				require.Equal(t, []map[string]any{{
					"level": "warn", "mock": ok[0].ID, "method": "GET", "route": "/x", "status": "200", "media_type": tt.media,
					"message": tt.warning,
				}}, warnings)
			}
		})
	}
}
