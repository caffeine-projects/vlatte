package openapiv3_test

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"
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
		Title: "Pets",
		Defs: map[string]any{"Pet": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"default": pet,
			},
			"discriminator": map[string]any{"propertyName": "name", "mapping": map[string]any{"rex": "#/$defs/Pet"}},
		}},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/pets/{id}", BasePath: "/v1", Summary: "Get a pet",
			PathParams: []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Name: "rex", Value: "1"}, {Value: "9"}}}},
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
    Name:
      type: string
`})

	require.Equal(t, map[string]any{
		"Age": map[string]any{
			"type": []any{"integer", "null"}, "exclusiveMinimum": int64(0), "maximum": int64(9), "example": int64(3),
		},
		"Name": map[string]any{"type": "string"},
	}, doc.Defs)
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
	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
servers: [{url: "/v1/"}, {url: "https://x.example.com/v2"}]
paths:
  /pets:
    get: {responses: {"200": {description: OK}}}
`})

	require.Equal(t, "/v1", doc.Operations[0].BasePath)
	require.Equal(t, []map[string]any{
		{"level": "warn", "message": `the servers have different paths: the mocks answer under the first one's, "/v1"`},
	}, warnings)
}

func TestLoad_ReadsHowAPathParameterIsWritten(t *testing.T) {
	// A path example is only matched as a client writes it: in the parameter's style, simple unless it says.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets/{id}/{tags}:
    get:
      parameters:
        - {name: id, in: path, required: true, style: matrix, schema: {type: integer}}
        - {name: tags, in: path, required: true, style: label, explode: true, schema: {type: array}}
      responses: {"200": {description: OK}}
`})

	require.Equal(t, []openapi.Parameter{
		{Name: "id", Style: "matrix"},
		{Name: "tags", Style: "label", Explode: true},
	}, doc.Operations[0].PathParams)
}

func TestLoad_PutsAnOperationUnderItsServersElseItsPathsElseTheDocuments(t *testing.T) {
	// OpenAPI lets a path, and an operation, replace the servers above them: each operation answers under its own.
	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
servers: [{url: /v1}]
paths:
  /owners:
    get: {responses: {"200": {description: OK}}}
  /pets:
    servers: [{url: /v2}, {url: /v4}]
    get: {responses: {"200": {description: OK}}}
    post:
      servers: [{url: /v3}, {url: /v5}]
      responses: {"200": {description: OK}}
`})

	bases := make(map[string]string, len(doc.Operations))
	for _, op := range doc.Operations {
		bases[op.Method+" "+op.Path] = op.BasePath
	}

	require.Equal(t, map[string]string{"GET /owners": "/v1", "GET /pets": "/v2", "POST /pets": "/v3"}, bases)
	require.Equal(t, []map[string]any{
		{"level": "warn", "path": "/pets", "message": `the servers have different paths: the mocks answer under the first one's, "/v2"`},
		{"level": "warn", "method": "POST", "path": "/pets", "message": `the servers have different paths: the mocks answer under the first one's, "/v3"`},
	}, warnings)
}

func TestLoad_WarnsOfAServerWhosePathIsNotAURL(t *testing.T) {
	_, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
servers: [{url: "https://x.example.com/{v}", variables: {v: {default: "%zz"}}}]
paths: {}
`})

	require.Equal(t, []map[string]any{{
		"level": "warn", "server": "https://x.example.com/{v}",
		"message": `the server's path is not a URL: parse "https://x.example.com/%zz": invalid URL escape "%zz": ignored`,
	}}, warnings)
}

func TestLoad_ReadsWhatASpecMayLeaveOut(t *testing.T) {
	// OpenAPI 3.1 makes paths and responses optional, and an example may live at an URL: none is an error.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
webhooks: {}
`})
	require.Empty(t, doc.Operations)

	doc = load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /a:
    get: {summary: A}
  /b:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              examples:
                far: {externalValue: "https://x.example.com/far.json"}
`})
	require.Equal(t, []openapi.Operation{
		{Method: "GET", Path: "/a", Summary: "A"},
		{Method: "GET", Path: "/b", Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "application/json"}}}}},
	}, sortedOperations(doc))
}

func TestLoad_TakesAnExampleOutsideJSONAsWritten(t *testing.T) {
	// OpenAPI writes the example of a media type that isn't JSON as the body itself, such as XML text, while its
	// schema describes the data: the example is no object, yet the spec is valid, wherever the response is used.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /a:
    get: {responses: {"200": {$ref: "#/components/responses/Pet"}}}
  /b:
    get: {responses: {"200": {$ref: "#/components/responses/Pet"}}}
components:
  responses:
    Pet:
      description: OK
      content:
        application/xml:
          schema: {type: object, properties: {name: {type: string}}}
          example: <Pet><name>Rex</name></Pet>
`})

	for _, op := range sortedOperations(doc) {
		require.Equal(t, []openapi.Example{{Value: "<Pet><name>Rex</name></Pet>"}}, op.Responses[0].Content[0].Examples, op.Path)
	}
}

func TestLoad_TakesAnExampleOutsideJSONOfAnEncodingHeaderAsWritten(t *testing.T) {
	// A part of a multipart body has headers of its own, whose content is written as the other bodies are.
	load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    post:
      requestBody:
        content:
          multipart/form-data:
            schema: {type: object, properties: {pet: {type: object}}}
            encoding:
              pet:
                headers:
                  X-Pet:
                    content:
                      application/xml:
                        schema: {type: object}
                        example: <Pet/>
      responses: {"204": {description: OK}}
`})
}

func TestLoad_FailsOnAnExampleOutsideJSONTheSpecForbids(t *testing.T) {
	// Only the schema check is left out of an example outside JSON: the rest of what OpenAPI asks of it holds.
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "an example and examples", content: "{schema: {type: object}, example: <Pet/>, examples: {a: {value: <Pet/>}}}", want: "example and examples are mutually exclusive"},
		{name: "a value and an external value", content: "{schema: {type: object}, examples: {a: {value: <Pet/>, externalValue: https://x.example.com/pet.xml}}}", want: "value and externalValue are mutually exclusive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(`
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    get:
      responses:
        "200":
          description: OK
          content:
            application/xml: ` + tt.content + "\n")

			log := zerolog.Nop()
			_, err := openapiv3.Load(t.Context(), data, &url.URL{Path: filepath.ToSlash(filepath.Join(t.TempDir(), "spec.yaml"))}, read(t), &log)
			require.ErrorIs(t, err, openapi.ErrInvalid)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoad_FailsOnAJSONExampleItsSchemaRejects(t *testing.T) {
	// A JSON example is the data its schema describes: one that breaks it would make a mock that breaks the contract.
	data := []byte(`
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
              schema: {type: object}
              example: <Pet/>
`)

	log := zerolog.Nop()
	_, err := openapiv3.Load(t.Context(), data, &url.URL{Path: filepath.ToSlash(filepath.Join(t.TempDir(), "spec.yaml"))}, read(t), &log)
	require.ErrorIs(t, err, openapi.ErrInvalid)
	require.ErrorContains(t, err, "invalid example")
}

func TestLoad_FailsOnAnInvalidSpec(t *testing.T) {
	// A contract that contradicts itself makes mocks that mislead.
	dir := t.TempDir()
	data := []byte("openapi: 3.1.0\ninfo: {title: Pets, version: \"1\"}\npaths:\n  /pets:\n    get:\n      parameters: [{name: a, in: querya}]\n      responses: {\"200\": {description: OK}}\n")

	log := zerolog.Nop()
	_, err := openapiv3.Load(t.Context(), data, &url.URL{Path: filepath.ToSlash(filepath.Join(dir, "spec.yaml"))}, read(t), &log)
	require.ErrorIs(t, err, openapi.ErrInvalid)
}

// sortedOperations returns the operations of doc in path order: Load leaves them in any order.
func sortedOperations(doc *openapi.Document) []openapi.Operation {
	return slices.SortedFunc(slices.Values(doc.Operations), func(a, b openapi.Operation) int { return strings.Compare(a.Path, b.Path) })
}

// load writes files to a folder and loads its spec.yaml, and fails on a warning.
func load(t *testing.T, files map[string]string) *openapi.Document {
	t.Helper()

	doc, warnings := loadWithWarnings(t, files)
	require.Empty(t, warnings)

	return doc
}

// loadWithWarnings writes files to a folder, loads its spec.yaml, and returns the entries Load logs.
func loadWithWarnings(t *testing.T, files map[string]string) (*openapi.Document, []map[string]any) {
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

	var logs bytes.Buffer

	log := zerolog.New(&logs)
	doc, err := openapiv3.Load(t.Context(), data, loc, r, &log)
	require.NoError(t, err)

	var entries []map[string]any

	for line := range bytes.Lines(logs.Bytes()) {
		var e map[string]any
		require.NoError(t, json.Unmarshal(line, &e))
		entries = append(entries, e)
	}

	return doc, entries
}

func read(t *testing.T) func(u *url.URL) ([]byte, error) {
	return func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }
}
