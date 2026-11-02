package openapiv3_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
)

func TestLoad_ReadsOperationsResponsesAndExamples(t *testing.T) {
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
servers: [{url: "https://api.example.com/v1"}]
paths:
  /pets/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}, example: "7"}
    get:
      summary: Get a pet
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string, examples: ["9"]}
          examples:
            rex: {value: "1"}
        - {name: q, in: query, schema: {type: string}, example: ignored}
      responses:
        "200":
          description: OK
          headers:
            X-Rate: {schema: {type: integer}, example: 5}
            Content-Type: {schema: {type: string}}
          content:
            application/xml:
              schema: {$ref: "#/components/schemas/Pet"}
            application/json:
              schema: {$ref: "#/components/schemas/Pet"}
              examples:
                rex: {value: {name: Rex}}
                cat: {value: {name: Tom}}
        4xx: {description: Bad}
        default: {description: Error}
components:
  schemas:
    Pet:
      type: object
      properties:
        name: {type: string}
        default: {$ref: "#/components/schemas/Pet"}
      discriminator: {propertyName: name, mapping: {rex: "#/components/schemas/Pet"}}
`})

	pet := map[string]any{"$ref": "#/$defs/Pet"}

	require.Equal(t, &openapi.Document{
		Title:    "Pets",
		BasePath: "/v1",
		Defs: map[string]any{"Pet": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"default": pet,
			},
			"discriminator": map[string]any{"propertyName": "name", "mapping": map[string]any{"rex": "#/$defs/Pet"}},
		}},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/pets/{id}", Summary: "Get a pet",
			PathParams: []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Name: "rex", Value: "1"}, {Value: "9"}}}},
			Responses: []openapi.Response{
				{
					Status: "200", Headers: []openapi.Header{{Name: "X-Rate", Schema: map[string]any{"type": "integer"}, Examples: []openapi.Example{{Value: float64(5)}}}},
					Content: []openapi.Content{
						{MediaType: "application/json", Schema: pet, Examples: []openapi.Example{
							{Name: "cat", Value: map[string]any{"name": "Tom"}}, {Name: "rex", Value: map[string]any{"name": "Rex"}},
						}},
						{MediaType: "application/xml", Schema: pet},
					},
				},
				{Status: "4XX"},
				{Status: "default"},
			},
		}},
	}, doc)
}

func TestLoad_WritesOpenAPI30SchemasAsJSONSchema2020(t *testing.T) {
	// OpenAPI 3.0 writes null and exclusive bounds its own way: fakefy reads JSON Schema 2020-12 only.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.0.3
info: {title: Pets, version: "1"}
paths: {}
components:
  schemas:
    Age:
      type: integer
      nullable: true
      minimum: 0
      exclusiveMinimum: true
      maximum: 9
      exclusiveMaximum: false
      example: 3
`})

	require.Equal(t, map[string]any{"Age": map[string]any{
		"type": []any{"integer", "null"}, "exclusiveMinimum": int64(0), "maximum": int64(9), "example": int64(3),
	}}, doc.Defs)
}

func TestLoad_ReadsTheFilesItsReferencesName(t *testing.T) {
	// A spec split in files refers to them relative to itself: the model names their schemas as shared ones.
	doc := load(t, map[string]string{
		"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: {$ref: "schemas/pet.yaml"}
`,
		"schemas/pet.yaml": "{type: object, properties: {name: {type: string}}}\n",
	})

	schema := doc.Operations[0].Responses[0].Content[0].Schema.(map[string]any)
	ref, ok := schema["$ref"].(string)
	require.True(t, ok)
	require.Equal(t, map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, doc.Defs[ref[len("#/$defs/"):]])
}

func TestLoad_WarnsWhenServersHaveDifferentPaths(t *testing.T) {
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
servers: [{url: "/v1/"}, {url: "https://x.example.com/v2"}]
paths: {}
`})

	require.Equal(t, "/v1", doc.BasePath)
	require.Equal(t, []string{`the servers have different paths: the mocks answer under the first one's, "/v1"`}, doc.Warnings)
}

func TestLoad_FailsOnAnInvalidSpec(t *testing.T) {
	// A contract that contradicts itself makes mocks that mislead.
	dir := t.TempDir()
	data := []byte("openapi: 3.1.0\ninfo: {title: Pets, version: \"1\"}\npaths:\n  /pets:\n    get:\n      parameters: [{name: a, in: querya}]\n      responses: {\"200\": {description: OK}}\n")

	_, err := openapiv3.Load(t.Context(), data, &url.URL{Path: filepath.ToSlash(filepath.Join(dir, "spec.yaml"))}, read(t))
	require.ErrorIs(t, err, openapi.ErrInvalid)
}

// load writes files to a folder and loads its spec.yaml.
func load(t *testing.T, files map[string]string) *openapi.Document {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	loc, err := openapi.Location(filepath.Join(dir, "spec.yaml"))
	require.NoError(t, err)

	r := read(t)

	data, err := r(loc)
	require.NoError(t, err)

	doc, err := openapiv3.Load(t.Context(), data, loc, r)
	require.NoError(t, err)

	return doc
}

func read(t *testing.T) func(u *url.URL) ([]byte, error) {
	return func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }
}
