package openapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestGenerate_WritesABodyInItsMediaType(t *testing.T) {
	// A mock file's body is text as is, and anything else as JSON: a value must reach the client in the media type
	// its Content-Type names.
	pet := map[string]any{"name": "Rex", "tags": []any{"a", "b"}, "note": "<&>"}

	tests := []struct {
		name   string
		media  string
		schema any
		value  any
		want   any
	}{
		{name: "a JSON object", media: "application/json", value: pet, want: pet},
		{name: "a JSON list", media: "application/vnd.api+json", value: []any{1}, want: []any{1}},
		{name: "JSON text", media: "application/json", value: "rex", want: `"rex"`},
		{name: "a JSON number", media: "application/json; charset=utf-8", value: 1.5, want: "1.5"},
		{name: "a JSON null", media: "application/json", value: nil, want: "null"},
		{
			name: "XML of an object", media: "application/xml", schema: map[string]any{"$ref": "#/$defs/Pet"}, value: pet,
			want: "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Pet><name>Rex</name><note>&lt;&amp;&gt;</note><tags>a</tags><tags>b</tags></Pet>",
		},
		{
			name: "XML of a list", media: "text/xml", value: []any{1, 2},
			schema: map[string]any{"xml": map[string]any{"name": "ids"}, "items": map[string]any{"xml": map[string]any{"name": "id"}}},
			want:   "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<ids><id>1</id><id>2</id></ids>",
		},
		{name: "XML text", media: "application/atom+xml", value: "<feed/>", want: "<feed/>"},
		{
			name: "XML of a list without a schema", media: "application/xml", value: []any{1, nil},
			want: "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<root><item>1</item><item></item></root>",
		},
		{
			name: "XML of an object of a schema without a name", media: "application/xml", schema: map[string]any{"type": "object"}, value: map[string]any{"a": nil},
			want: "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<root><a></a></root>",
		},
		{name: "JSON of a media type with parameters", media: "Application/JSON ; charset=utf-8", value: true, want: "true"},
		{name: "plain text", media: "text/plain", value: 42, want: "42"},
		{name: "other text", media: "application/octet-stream", value: "AAEC", want: "AAEC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
				Method: "GET", Path: "/x",
				Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
					{MediaType: tt.media, Schema: tt.schema, Examples: []openapi.Example{{Value: tt.value}}},
				}}},
			}}})

			var ok []openapi.Mock
			for _, m := range mocks {
				if m.Status == "200" {
					ok = append(ok, m)
				}
			}

			require.Len(t, ok, 1)
			require.Equal(t, tt.want, ok[0].Reply.Body)
			require.Equal(t, tt.media, ok[0].Reply.Header["Content-Type"])
		})
	}
}
