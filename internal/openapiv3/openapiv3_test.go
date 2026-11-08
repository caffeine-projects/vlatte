package openapiv3_test

import (
	"bytes"
	"cmp"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/internal/value"
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
					Status: "200", Headers: []openapi.Header{{Name: "X-Rate", Schema: map[string]any{"type": "integer"}, Examples: []openapi.Example{{Value: int64(5)}}}},
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

func TestLoad_ReadsExamplesAsTheSpecWritesThem(t *testing.T) {
	// kin-openapi reads every number as a float64 and a null as no value: an integer past 2^53 would name another
	// resource, and a null example would be no example. Each file's references are relative to it.
	doc := load(t, map[string]string{
		"spec.yaml": `
openapi: 3.1.0
info: {title: Big, version: "1"}
paths:
  /ids/{id}:
    get:
      parameters: [{$ref: "param.yaml"}]
      responses:
        "200": {$ref: "responses.yaml#/OK"}
`,
		"param.yaml": `
name: id
in: path
required: true
example: 9007199254740993
schema: {type: integer, example: 9007199254740995, examples: [9007199254740997]}
`,
		"responses.yaml": `
OK:
  description: OK
  headers:
    X-ID: {schema: {type: integer}, example: 9007199254740993}
  content:
    application/json:
      examples:
        big: {$ref: "#/Big"}
        none: {value: null}
        out: {externalValue: "https://example.com/pet.json"}
    text/plain:
      example: null
Big: {value: {id: 9007199254740999}}
`,
	})

	require.Equal(t, []openapi.Operation{{
		Method: "GET", Path: "/ids/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{
			{Value: int64(9007199254740993)}, {Value: int64(9007199254740995)}, {Value: int64(9007199254740997)},
		}}},
		Responses: []openapi.Response{{
			Status:  "200",
			Headers: []openapi.Header{{Name: "X-ID", Schema: map[string]any{"type": "integer"}, Examples: []openapi.Example{{Value: int64(9007199254740993)}}}},
			Content: []openapi.Content{
				{MediaType: "application/json", Examples: []openapi.Example{
					{Name: "big", Value: map[string]any{"id": int64(9007199254740999)}}, {Name: "none", Value: nil},
				}},
				{MediaType: "text/plain", Examples: []openapi.Example{{Value: nil}}},
			},
		}},
	}}, doc.Operations)
}

func TestLoad_TakesANullExampleWhereverTheSpecPutsIt(t *testing.T) {
	// kin-openapi reads value: null as no value, which it rejects: each place it checks an example is read as written.
	const (
		null    = `{schema: {type: ["null"]}, examples: {none: {value: null}}}`
		content = `{application/json: ` + null + `}`
		ok      = `{"204": {description: OK}}`
	)

	tests := []struct {
		name    string
		spec    string
		version string
		files   map[string]string // more files of the spec
	}{
		{name: "a shared parameter", spec: "paths: {}\ncomponents: {parameters: {P: {name: p, in: query, " + null[1:] + "}}"},
		{name: "a shared request body", spec: "paths: {}\ncomponents: {requestBodies: {B: {content: " + content + "}}}"},
		{name: "a shared response", spec: "paths: {}\ncomponents: {responses: {R: {description: R, content: " + content + "}}}"},
		{name: "a shared callback", spec: "paths: {}\ncomponents: {callbacks: {C: {\"{$url}\": {post: {requestBody: {content: " + content + "}, responses: " + ok + "}}}}}"},
		{name: "a webhook", spec: "paths: {}\nwebhooks: {w: {post: {requestBody: {content: " + content + "}, responses: " + ok + "}}}"},
		{
			name:  "a parameter a pointer names",
			spec:  "paths: {/a: {get: {parameters: [{$ref: \"params.yaml#/by~1name/0\"}], responses: " + ok + "}}}",
			files: map[string]string{"params.yaml": "by/name: [{name: p, in: query, " + null[1:] + "]\n"},
		},
		{name: "an additional operation", spec: "paths: {/a: {additionalOperations: {COPY: {requestBody: {content: " + content + "}, responses: " + ok + "}}}}", version: "3.2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"spec.yaml": "openapi: " + cmp.Or(tt.version, "3.1.0") + "\ninfo: {title: X, version: \"1\"}\n" + tt.spec + "\n"}
			maps.Copy(files, tt.files)
			load(t, files)
		})
	}
}

func TestLoad_ChecksANullExampleAgainstItsSchema(t *testing.T) {
	// null is a value, which its schema checks as any other, wherever the spec writes it: kin-openapi reads it as no
	// value, which it doesn't check. A default is checked as an example.
	tests := []struct {
		name  string
		place string // where the null is, with %s for the type of its schema
		error string
	}{
		{
			name:  "a named example",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: %s}, examples: {none: {value: null}}}}}}",
			error: `example none: validation failed due to: at '': got null, want object`,
		},
		{
			name:  "the example of a media type",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: %s}, example: null}}}}",
			error: `invalid example of spec.yaml#/paths/~1pets/get/responses/200/content/application~1json: `,
		},
		{
			name:  "the example of a parameter",
			place: "parameters: [{name: q, in: query, schema: {type: %s}, example: null}]\n      responses: {\"204\": {description: OK}}",
			error: `invalid example of spec.yaml#/paths/~1pets/get/parameters/0: `,
		},
		{
			name:  "the example of a header",
			place: "responses: {\"204\": {description: OK, headers: {X-A: {schema: {type: %s}, example: null}}}}",
			error: `invalid example of spec.yaml#/paths/~1pets/get/responses/204/headers/X-A: `,
		},
		{
			name:  "the example of a schema",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/S\"}}}}}\ncomponents: {schemas: {S: {type: %s, example: null}}}",
			error: `invalid example of spec.yaml#/components/schemas/S: `,
		},
		{
			name:  "the default of a schema",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {properties: {a: {type: %s, default: null}}}}}}}",
			error: `invalid default of spec.yaml#/paths/~1pets/get/responses/200/content/application~1json/schema/properties/a: `,
		},
		{
			name: "the example of a schema beside a $ref",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/Any\", properties: {a: {type: %s, example: null}}}}}}}" +
				"\ncomponents: {schemas: {Any: {}}}",
			error: `invalid example of spec.yaml#/paths/~1pets/get/responses/200/content/application~1json/schema/properties/a: `,
		},
		// An example outside JSON is the body, which its schema doesn't describe, and examples is a list.
		{
			name:  "the example of a media type outside JSON",
			place: "responses: {\"200\": {description: OK, content: {text/plain: {schema: {type: %s}, example: null}}}}",
		},
		{
			name:  "the examples of a schema",
			place: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: %s, examples: null}}}}}",
		},
	}

	nop := zerolog.Nop()

	for _, tt := range tests {
		spec := func(types string) map[string]string {
			return map[string]string{"spec.yaml": "openapi: 3.1.0\ninfo: {title: Pets, version: \"1\"}\npaths:\n  /pets:\n    get:\n      " +
				fmt.Sprintf(tt.place, types) + "\n"}
		}

		t.Run(tt.name, func(t *testing.T) {
			_, err := loadFiles(t, spec("object"), &nop)
			if tt.error == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, openapi.ErrInvalid)
			require.ErrorContains(t, err, tt.error)
			require.ErrorContains(t, err, "got null, want object")
		})

		t.Run(tt.name+" its schema allows", func(t *testing.T) {
			_, err := loadFiles(t, spec(`[object, "null"]`), &nop)
			require.NoError(t, err)
		})
	}
}

func TestLoad_NamesTheFirstNullItsSchemaRejectsOnEveryRun(t *testing.T) {
	// Of the nulls a spec gets wrong, the one first in it is named, the same on every run.
	files := map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    get:
      parameters: [{name: q, in: query, schema: {type: object}, example: null}]
      responses:
        "200":
          description: OK
          headers: {X-A: {schema: {type: object}, example: null}}
          content: {application/json: {schema: {type: object}, example: null}}
`}

	nop := zerolog.Nop()

	for range 10 {
		_, err := loadFiles(t, files, &nop)
		require.ErrorContains(t, err, `invalid example of spec.yaml#/paths/~1pets/get/parameters/0: `)
	}
}

func TestLoad_ReadsAReferenceToAnythingOfASpec(t *testing.T) {
	// A $ref may name any part of a spec, not only a shared component: kin-openapi's own naming of what it names
	// loops forever on one into paths.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Refs, version: "1"}
paths:
  /a/{id}:
    get:
      parameters: [{name: id, in: path, required: true, schema: {type: string}, example: "7"}]
      responses: {"204": {description: OK}}
  /b/{id}:
    get:
      parameters: [{$ref: "#/paths/~1a~1{id}/get/parameters/0"}]
      responses: {"204": {description: OK}}
`})

	param := []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Value: "7"}}}}
	require.Equal(t, []openapi.Operation{
		{Method: "GET", Path: "/a/{id}", PathParams: param, Responses: []openapi.Response{{Status: "204"}}},
		{Method: "GET", Path: "/b/{id}", PathParams: param, Responses: []openapi.Response{{Status: "204"}}},
	}, sortedOperations(doc))
}

func TestLoad_NamesASchemaOfAnotherFileApartFromAShared(t *testing.T) {
	// A schema of another file becomes a shared one, named after its file and its place in it: one already shared
	// under that name keeps its own, and every reference to the same schema names the same one.
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
              schema:
                type: object
                properties:
                  a: {$ref: "pet.yaml"}
                  b: {$ref: "pet.yaml"}
                  c: {$ref: "#/components/schemas/pet"}
                  d: {$ref: "lib.yaml#/components/schemas/Owner"}
components:
  schemas:
    pet: {const: shared}
`,
		"pet.yaml": "{const: file}\n",
		"lib.yaml": "components: {schemas: {Owner: {items: {$ref: \"spec.yaml#/components/schemas/pet\"}}}}\n",
	})

	props := doc.Operations[0].Responses[0].Content[0].Schema.(map[string]any)["properties"].(map[string]any)
	require.Equal(t, map[string]any{
		"a": map[string]any{"$ref": "#/$defs/pet_2"},
		"b": map[string]any{"$ref": "#/$defs/pet_2"},
		"c": map[string]any{"$ref": "#/$defs/pet"},
		"d": map[string]any{"$ref": "#/$defs/lib_Owner"},
	}, props)
	require.Equal(t, map[string]any{
		"pet":       map[string]any{"const": "shared"},
		"pet_2":     map[string]any{"const": "file"},
		"lib_Owner": map[string]any{"items": map[string]any{"$ref": "#/$defs/pet"}},
	}, doc.Defs)
}

func TestLoad_ReadsTheNumbersOfASchemaAsTheSpecWritesThem(t *testing.T) {
	// A fake value comes from its schema's const, enum or default: 9007199254740993 read as a float64 is another
	// value, and the largest int64 one past it. Shared schemas, those of other files and keywords beside a $ref alike.
	// The largest was found by TestPropLoad_AValueIsTheSameWhereverItsReferencesLead.
	doc := load(t, map[string]string{
		"spec.yaml": `
openapi: 3.1.0
info: {title: Big, version: "1"}
paths:
  /big:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  a: {const: 9007199254740993}
                  b: {$ref: "#/components/schemas/B"}
                  c: {$ref: "c.yaml"}
                  d: {const: 9223372036854775807}
        "201":
          description: Created
          content:
            application/json:
              schema: {$ref: "#/components/schemas/E", const: 9007199254740993}
components:
  schemas:
    B: {enum: [9007199254740995], default: 9007199254740997}
    E: {type: integer}
    F: {$ref: "#/components/schemas/E", const: 9007199254740993}
    G: {$ref: "#/components/schemas/E", default: 9007199254740993}
`,
		"c.yaml": "{type: array, items: {const: {n: 9007199254740999}}}\n",
	})

	schema := doc.Operations[0].Responses[0].Content[0].Schema.(map[string]any)
	props := schema["properties"].(map[string]any)
	require.Equal(t, map[string]any{"const": int64(9007199254740993)}, props["a"])
	require.Equal(t, map[string]any{"const": int64(math.MaxInt64)}, props["d"])
	// A shared schema's $ref is resolved, with the keywords beside it.
	require.Equal(t, map[string]any{"$ref": "#/$defs/E", "const": int64(9007199254740993)}, doc.Operations[0].Responses[1].Content[0].Schema)
	require.Equal(t, map[string]any{"type": "integer", "allOf": []any{map[string]any{"const": int64(9007199254740993)}}}, doc.Defs["F"])
	require.Equal(t, map[string]any{"type": "integer", "default": int64(9007199254740993)}, doc.Defs["G"])
	require.Equal(t, map[string]any{"enum": []any{int64(9007199254740995)}, "default": int64(9007199254740997)}, doc.Defs["B"])

	c := strings.TrimPrefix(props["c"].(map[string]any)["$ref"].(string), "#/$defs/")
	require.Equal(t, map[string]any{"type": "array", "items": map[string]any{"const": map[string]any{"n": int64(9007199254740999)}}}, doc.Defs[c])
}

func TestLoad_KeepsTheNullsOfASchema(t *testing.T) {
	// A schema whose const is null allows null only, not anything: kin-openapi reads a null as no value. Found by
	// TestPropLoad_AValueIsTheSameWhereverItsReferencesLead.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Nulls, version: "1"}
paths:
  /null:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  a: {const: null, default: null, example: null}
components:
  schemas:
    Any: {}
    NullConst: {$ref: "#/components/schemas/Any", const: null}
`})

	require.Equal(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"const": nil, "default": nil, "example": nil}},
	}, doc.Operations[0].Responses[0].Content[0].Schema)

	// kin-openapi resolves the $ref of a shared schema, with the keywords beside it, which assert, in an allOf entry:
	// one it leaves out when they are null. Found by TestPropLoad_AValueIsTheSameWhereverItsReferencesLead.
	require.Equal(t, map[string]any{"allOf": []any{map[string]any{"const": nil}}}, doc.Defs["NullConst"])
}

func TestLoad_KeepsANullBesideARefToItsOwnSchema(t *testing.T) {
	// A null beside a $ref is the schema's own, not that of the schema an allOf of the referenced one names:
	// kin-openapi adds no allOf entry for keywords beside a $ref that are all null.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Nulls, version: "1"}
paths:
  /p:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: {$ref: "#/components/schemas/T", const: null}
components:
  schemas:
    Base: {type: object}
    T: {allOf: [{$ref: "#/components/schemas/Base"}]}
`})

	require.Equal(t, map[string]any{"type": "object"}, doc.Defs["Base"])
	require.Equal(t, map[string]any{"$ref": "#/$defs/T", "const": nil}, doc.Operations[0].Responses[0].Content[0].Schema)
}

func TestLoad_ReadsABooleanSchema(t *testing.T) {
	// OpenAPI 3.1 takes a boolean wherever a schema goes: true allows any value, false none.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Booleans, version: "1"}
paths:
  /any:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json: {schema: true}
  /none:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json: {schema: {$ref: "#/components/schemas/Never"}}
  /object:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json: {schema: {type: object, properties: {a: true, b: false}}}
components:
  schemas:
    Never: false
`})

	schemas := make(map[string]any, len(doc.Operations))
	for _, op := range doc.Operations {
		schemas[op.Path] = followDefs(t, doc, op.Responses[0].Content[0].Schema)
	}

	require.Equal(t, map[string]any{
		"/any":    true,
		"/none":   false,
		"/object": map[string]any{"type": "object", "properties": map[string]any{"a": true, "b": false}},
	}, schemas)
}

func TestLoad_ReadsTheNumbersAtTheEndOfAChainOfReferencesOfAnyLength(t *testing.T) {
	// A chain of references, however long, ends at a schema, which keeps 9007199254740993 as written.
	const length = 40

	var spec strings.Builder

	spec.WriteString(`
openapi: 3.1.0
info: {title: Chain, version: "1"}
paths:
  /big:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json: {schema: {$ref: "#/components/schemas/S0"}}
components:
  schemas:
`)

	for i := range length {
		fmt.Fprintf(&spec, "    S%d: {$ref: \"#/components/schemas/S%d\"}\n", i, i+1)
	}

	fmt.Fprintf(&spec, "    S%d: {type: integer, const: 9007199254740993}\n", length)

	doc := load(t, map[string]string{"spec.yaml": spec.String()})

	require.Equal(t,
		map[string]any{"type": "integer", "const": int64(9007199254740993)},
		followDefs(t, doc, doc.Operations[0].Responses[0].Content[0].Schema))
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

func TestLoad_WarnsOfWhatTheSpecGetsWrongButLeavesNoMockWrong(t *testing.T) {
	// Real specs hold such slips: OpenAI's an unknown keyword, GitHub's two paths that differ only in a parameter's
	// name. Failing the whole spec over one leaves no mocks at all.
	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.0.3
info: {title: Pets, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: OK, content: {application/json: {schema: {$ref: "#/components/schemas/Pet"}}}}}
  /pets/{name}:
    put:
      parameters: [{name: name, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: Saved}}
components:
  schemas:
    Pet:
      type: object
      properties:
        name: {type: string, optional: true}
`})

	ops := make([]string, 0, len(doc.Operations))
	for _, o := range doc.Operations {
		ops = append(ops, o.Method+" "+o.Path)
	}

	require.Equal(t, []string{"GET /pets/{id}", "PUT /pets/{name}"}, ops)
	require.Equal(t, []map[string]any{
		{"level": "warn", "message": `invalid components: schema "Pet": extra sibling fields: [optional]: ignored`},
		{"level": "warn", "message": "invalid paths: invalid path /pets/{id}: invalid operation GET: extra sibling fields: [optional]: ignored"},
		{"level": "warn", "message": `invalid paths: conflicting paths "/pets/{name}" and "/pets/{id}": their mocks of one method answer the same requests`},
	}, warnings)
}

func TestLoad_FailsOnWhatAWarningWouldHide(t *testing.T) {
	// kin-openapi stops checking a schema at its first problem, and skips a path that conflicts with another: what is
	// wrong past a slip it only warns of must still fail the spec.
	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "a schema past an unknown keyword",
			spec: `
openapi: 3.0.3
info: {title: Pets, version: "1"}
paths: {}
components:
  schemas:
    Pet:
      type: object
      properties:
        a: {type: string, optional: true}
        b: {type: strin}
`,
			want: `unsupported 'type' value "strin"`,
		},
		{
			name: "a path that conflicts with another",
			spec: `
openapi: 3.0.3
info: {title: Pets, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: Done}}
  /pets/{name}:
    put:
      parameters: [{name: nme, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: Saved}}
`,
			want: "must define exactly all path parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := zerolog.Nop()
			_, err := loadFiles(t, map[string]string{"spec.yaml": tt.spec}, &log)
			require.ErrorIs(t, err, openapi.ErrInvalid)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoad_KeepsAPatternGoCannotCompile(t *testing.T) {
	// JSON Schema patterns are ECMA-262 regular expressions: Go's regexp has no lookahead, which OpenAI's spec uses.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    get:
      responses: {"200": {description: OK, content: {application/json: {schema: {type: string, pattern: "^(?!x)"}}}}}
`})

	require.Equal(t, map[string]any{"type": "string", "pattern": "^(?!x)"}, doc.Operations[0].Responses[0].Content[0].Schema)
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

func TestLoad_ReadsTheExamplesOfAPathParameterWithContent(t *testing.T) {
	// A parameter with content is written in its media type: its own examples, then its media type's, then its
	// schema's, are its values, as the spec writes them. The values beside a $ref to a schema are its own.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets/{id}/{tag}:
    get:
      parameters:
        - name: id
          in: path
          required: true
          example: 9007199254740991
          content:
            application/json:
              schema: {type: integer, examples: [9007199254740995]}
              example: 9007199254740993
        - name: tag
          in: path
          required: true
          content:
            text/plain:
              schema: {$ref: "#/components/schemas/Tag", example: own}
              examples:
                a: {value: a}
      responses: {"200": {description: OK}}
components:
  schemas:
    Tag: {type: string, example: shared}
`})

	require.Equal(t, []openapi.Parameter{
		{Name: "id", Style: "simple", MediaType: "application/json", Examples: []openapi.Example{
			{Value: int64(9007199254740991)}, {Value: int64(9007199254740993)}, {Value: int64(9007199254740995)},
		}},
		{Name: "tag", Style: "simple", MediaType: "text/plain", Examples: []openapi.Example{{Name: "a", Value: "a"}, {Value: "own"}}},
	}, doc.Operations[0].PathParams)
}

func TestLoad_IgnoresTheKeywordsBesideARefInOpenAPI30(t *testing.T) {
	// In OpenAPI 3.0 a $ref replaces its object: what is beside it is no part of the schema.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.0.3
info: {title: Pets, version: "1"}
paths:
  /pets/{tag}:
    get:
      parameters:
        - {name: tag, in: path, required: true, schema: {$ref: "#/components/schemas/Tag", example: own, default: null, maxLength: 3}}
      responses: {"200": {description: OK}}
components:
  schemas:
    Tag: {type: string, example: shared}
`})

	require.Equal(t, []openapi.Example{{Value: "shared"}}, doc.Operations[0].PathParams[0].Examples)
	require.Equal(t, map[string]any{"type": "string", "example": "shared"}, doc.Defs["Tag"])
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

func TestLoad_ResolvesAServerURLAgainstWhereItsFileIsServed(t *testing.T) {
	// OpenAPI resolves a server's URL as a link of the file that holds it: v1 is beside the file, and
	// //api.example.test/v3 names a host, whose path is /v3. What is written beside a path's $ref is the referring
	// file's, but for an empty list, which kin-openapi passes over. A spec read from disk is served from the root, its
	// folder as /, which a file outside it is under too.
	dir := t.TempDir()
	for name, content := range map[string]string{
		"docs/spec.yaml": `
openapi: 3.1.0
info: {title: Servers, version: "1"}
servers: [{url: v1}]
paths:
  /a:
    get: {responses: {"200": {description: OK}}}
  /b: {$ref: "paths/b.yaml"}
  /c:
    get:
      servers: [{url: "//api.example.test/v3"}]
      responses: {"200": {description: OK}}
  /d:
    $ref: "paths/b.yaml"
    servers: [{url: v4}]
    post:
      servers: [{url: v5}]
      responses: {"200": {description: OK}}
  /e: {$ref: "../shared/e.yaml"}
  /f: {$ref: "paths/b.yaml", servers: []}
`,
		"docs/paths/b.yaml": `
servers: [{url: v2}]
get: {responses: {"200": {description: OK}}}
`,
		"shared/e.yaml": `
servers: [{url: v6}]
get: {responses: {"200": {description: OK}}}
`,
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	// bases loads the spec at loc and returns the base path of each of its operations.
	bases := func(t *testing.T, loc *url.URL) map[string]string {
		data, err := read(t)(loc)
		require.NoError(t, err)

		var logs bytes.Buffer

		log := zerolog.New(&logs)
		doc, err := openapiv3.Load(t.Context(), data, loc, read(t), &log)
		require.NoError(t, err)
		require.Empty(t, logs.String())

		out := make(map[string]string, len(doc.Operations))
		for _, op := range doc.Operations {
			out[op.Method+" "+op.Path] = op.BasePath
		}

		return out
	}

	t.Run("from disk", func(t *testing.T) {
		loc, err := openapi.Location(filepath.Join(dir, "docs", "spec.yaml"))
		require.NoError(t, err)

		require.Equal(t, map[string]string{
			"GET /a": "/v1", "GET /b": "/paths/v2", "GET /c": "/v3", "GET /d": "/v4", "POST /d": "/v5", "GET /e": "/shared/v6",
			"GET /f": "/paths/v2",
		}, bases(t, loc))
	})

	t.Run("over http", func(t *testing.T) {
		srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
		t.Cleanup(srv.Close)

		loc, err := url.Parse(srv.URL + "/docs/spec.yaml")
		require.NoError(t, err)

		require.Equal(t, map[string]string{
			"GET /a": "/docs/v1", "GET /b": "/docs/paths/v2", "GET /c": "/v3", "GET /d": "/docs/v4", "POST /d": "/docs/v5",
			"GET /e": "/shared/v6", "GET /f": "/docs/paths/v2",
		}, bases(t, loc))
	})
}

func TestLoad_ReadsASpecAtAWindowsPath(t *testing.T) {
	// A Windows path has a drive and no leading slash, C:/api/spec.yaml: the files it refers to are read as written,
	// and its folder is / all the same.
	files := map[string]string{
		"C:/api/spec.yaml": `
openapi: 3.1.0
info: {title: Servers, version: "1"}
servers: [{url: v1}]
paths:
  /a:
    get: {responses: {"200": {description: OK}}}
  /b: {$ref: "paths/b.yaml"}
`,
		"C:/api/paths/b.yaml": `
servers: [{url: v2}]
get:
  responses:
    "200":
      description: OK
      content:
        application/json: {example: 9007199254740993}
`,
	}
	read := func(u *url.URL) ([]byte, error) {
		content, ok := files[strings.TrimPrefix(strings.TrimPrefix(u.Path, "./"), "/")]
		if !ok {
			return nil, fmt.Errorf("no file %s", u)
		}

		return []byte(content), nil
	}

	loc := &url.URL{Path: "C:/api/spec.yaml"}
	log := zerolog.Nop()
	doc, err := openapiv3.Load(t.Context(), []byte(files[loc.Path]), loc, read, &log)
	require.NoError(t, err)

	bases := make(map[string]string, len(doc.Operations))
	for _, op := range doc.Operations {
		bases[op.Path] = op.BasePath
	}

	require.Equal(t, map[string]string{"/a": "/v1", "/b": "/paths/v2"}, bases)
	require.Equal(t, []openapi.Example{{Value: int64(9007199254740993)}}, operation(t, doc, "/b").Responses[0].Content[0].Examples)
}

func TestLoad_ReadsWhatIsWrittenBesideAPathsRef(t *testing.T) {
	// kin-openapi puts what is written beside a path's $ref over the path it refers to: its parameters and operations
	// are read from the referring file, 9007199254740993 included.
	doc := load(t, map[string]string{
		"spec.yaml": `
openapi: 3.1.0
info: {title: Beside, version: "1"}
paths:
  /b/{id}:
    $ref: "paths/b.yaml"
    parameters: [{name: id, in: path, required: true, schema: {type: integer}, example: 9007199254740993}]
    post:
      responses:
        "200":
          description: OK
          content:
            application/json: {example: 9007199254740995}
`,
		"paths/b.yaml": `
parameters: [{name: id, in: path, required: true, schema: {type: integer}, example: 1}]
get: {responses: {"200": {description: OK}}}
`,
	})

	params := make(map[string][]openapi.Example, len(doc.Operations))
	for _, op := range doc.Operations {
		params[op.Method] = op.PathParams[0].Examples
	}

	big := []openapi.Example{{Value: int64(9007199254740993)}}
	require.Equal(t, map[string][]openapi.Example{"GET": big, "POST": big}, params)

	post := doc.Operations[slices.IndexFunc(doc.Operations, func(o openapi.Operation) bool { return o.Method == "POST" })]
	require.Equal(t, []openapi.Example{{Value: int64(9007199254740995)}}, post.Responses[0].Content[0].Examples)
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

func TestLoad_TakesAnExampleOutsideJSONAsWrittenWhereverTheSpecPutsIt(t *testing.T) {
	// A spec may give a body anywhere: in a webhook, a callback, or a component its operations share.
	xml := "{content: {application/xml: {schema: {type: object}, example: <Pet/>}}}"
	load(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    parameters: [{$ref: "#/components/parameters/Filter"}]
    post:
      requestBody: {$ref: "#/components/requestBodies/Pet"}
      callbacks: {created: {$ref: "#/components/callbacks/Created"}}
      responses: {"204": {description: OK, headers: {X-Pet: {$ref: "#/components/headers/Pet"}}}}
    put:
      callbacks: {created: {$ref: "#/components/callbacks/Created"}}
      responses: {"204": {description: OK}}
webhooks:
  pet:
    post:
      requestBody: ` + xml + `
      responses: {"204": {description: OK}}
components:
  parameters:
    Filter: {name: filter, in: query, ` + xml[1:] + `
  headers:
    Pet: ` + xml + `
  requestBodies:
    Pet: ` + xml + `
  callbacks:
    Created:
      "{$request.body#/url}":
        post:
          requestBody: ` + xml + `
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

func TestPropLoad_AValueIsTheSameWhereverItsReferencesLead(t *testing.T) {
	// R1: a parameter, a response, a named example and a schema mean the same inline or behind any chain of $refs,
	// to the spec's components, another file, a file in a sub folder, a whole file, or a parameter of another path,
	// each relative to the file that holds it: every value they hold, integers past 2^53 and nulls included, is the
	// spec's own, and so is a keyword beside a $ref.
	rapid.Check(t, func(rt *rapid.T) {
		s := newDrawnSpec()
		v1, v2, v3, v4 := genJSON(2).Draw(rt, "example"), genJSON(2).Draw(rt, "header"), genJSON(2).Draw(rt, "parameter"), genJSON(2).Draw(rt, "const")
		v5, v6 := genJSON(2).Draw(rt, "const beside a $ref"), genJSON(2).Draw(rt, "const beside the $ref of a shared schema")

		paramAt := s.draw(rt, "parameters")
		param := s.put(paramAt, "spec.yaml", map[string]any{
			"name": "id", "in": "path", "required": true, "schema": make(map[string]any), "example": v3,
		})

		responseAt := s.draw(rt, "responses")
		holder := s.holder(responseAt, "spec.yaml")
		response := s.put(responseAt, "spec.yaml", map[string]any{
			"description": "OK",
			"headers":     map[string]any{"X-V": map[string]any{"schema": make(map[string]any), "example": v2}},
			"content": map[string]any{
				"application/json":   map[string]any{"examples": map[string]any{"e": s.put(s.draw(rt, "examples"), holder, map[string]any{"value": v1})}},
				"application/x+json": map[string]any{"schema": s.put(s.draw(rt, "schemas"), holder, map[string]any{"const": v4})},
				"application/y+json": map[string]any{"schema": s.beside(rt, holder, v5)},
			},
		})
		s.components("schemas")["Beside"] = s.beside(rt, "spec.yaml", v6)
		s.components("schemas")["Empty"] = make(map[string]any)

		s.paths()["/x/{id}"] = map[string]any{"get": map[string]any{"parameters": []any{param}, "responses": map[string]any{"200": response}}}

		doc := s.load(t, rt)
		op := operation(rt, doc, "/x/{id}")

		require.Equal(rt, []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Value: normal(rt, v3)}}}}, op.PathParams)
		require.Len(rt, op.Responses, 1)

		res := op.Responses[0]
		require.Equal(rt, []openapi.Header{{Name: "X-V", Schema: make(map[string]any), Examples: []openapi.Example{{Value: normal(rt, v2)}}}}, res.Headers)
		require.Len(rt, res.Content, 3)
		require.Equal(rt, openapi.Content{MediaType: "application/json", Examples: []openapi.Example{{Name: "e", Value: normal(rt, v1)}}}, res.Content[0])
		require.Equal(rt, "application/x+json", res.Content[1].MediaType)
		require.Equal(rt, map[string]any{"const": normal(rt, v4)}, followDefs(rt, doc, res.Content[1].Schema))
		require.Equal(rt, normal(rt, v5), besideConst(rt, res.Content[2].Schema))
		require.Equal(rt, normal(rt, v6), besideConst(rt, doc.Defs["Beside"]))
		require.Equal(rt, make(map[string]any), doc.Defs["Empty"])
	})
}

func TestPropLoad_EachSchemaAReferenceNamesIsItsOwn(t *testing.T) {
	// R2: schemas of other files become shared ones, named after their file and place: whatever names the spec's
	// own shared schemas take, two references to one schema name one, and to two schemas two, each the schema
	// written there.
	rapid.Check(t, func(rt *rapid.T) {
		s := newDrawnSpec()
		names := []string{"pet", "pet_2", "a_pet", "a_pet_2", "lib_pet"}

		for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom(names), 0, len(names), rapid.ID).Draw(rt, "shared") {
			s.components("schemas")[name] = map[string]any{"const": "#/components/schemas/" + name}
		}

		targets := []string{"pet.yaml", "pet_2.yaml", "a/pet.yaml", "a_pet.yaml", "a/pet_2.yaml", "lib.yaml#/components/schemas/pet", "lib.yaml#/pet"}
		for _, target := range targets {
			file, ptr, _ := strings.Cut(target, "#")
			if s.files[file] == nil {
				s.files[file] = make(map[string]any)
			}

			if ptr == "" {
				s.files[file]["const"] = target
				continue
			}

			setPointer(s.files[file], ptr, map[string]any{"const": target})
		}

		refs := rapid.SliceOfN(rapid.SampledFrom(slices.Concat(targets, slices.Collect(maps.Keys(s.components("schemas"))))), 1, 12).Draw(rt, "refs")

		props := make(map[string]any, len(refs))
		for i, ref := range refs {
			if !strings.Contains(ref, ".") {
				ref = "#/components/schemas/" + ref
			}

			props[strconv.Itoa(i)] = map[string]any{"$ref": ref}
			refs[i] = ref
		}

		s.paths()["/x"] = map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{
			"description": "OK",
			"content":     map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props}}},
		}}}}

		doc := s.load(t, rt)
		got := operation(rt, doc, "/x").Responses[0].Content[0].Schema.(map[string]any)["properties"].(map[string]any)

		byRef := make(map[string]any)
		for i, ref := range refs {
			// Each schema's const is the $ref that names it.
			name := got[strconv.Itoa(i)]
			require.Equal(rt, map[string]any{"const": ref}, followDefs(rt, doc, name), ref)

			if prev, ok := byRef[ref]; ok {
				require.Equal(rt, prev, name, ref)
			}

			byRef[ref] = name
		}

		seen := make(map[any]string)
		for ref, name := range byRef {
			key := name.(map[string]any)["$ref"]
			prev, ok := seen[key]
			require.False(rt, ok, "%s and %s both name %s", prev, ref, key)
			seen[key] = ref
		}
	})
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

	var logs bytes.Buffer

	log := zerolog.New(&logs)
	doc, err := loadFiles(t, files, &log)
	require.NoError(t, err)

	var entries []map[string]any

	for line := range bytes.Lines(logs.Bytes()) {
		var e map[string]any
		require.NoError(t, json.Unmarshal(line, &e))
		entries = append(entries, e)
	}

	return doc, entries
}

// loadFiles writes files to a folder and loads its spec.yaml, which logs on log.
func loadFiles(t *testing.T, files map[string]string, log *zerolog.Logger) (*openapi.Document, error) {
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

	return openapiv3.Load(t.Context(), data, loc, r, log)
}

func read(t *testing.T) func(u *url.URL) ([]byte, error) {
	return func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }
}

// operation returns the operation of doc at path.
func operation(t require.TestingT, doc *openapi.Document, path string) openapi.Operation {
	i := slices.IndexFunc(doc.Operations, func(o openapi.Operation) bool { return o.Path == path })
	require.NotEqual(t, -1, i, path)

	return doc.Operations[i]
}

// followDefs returns schema, or the shared schema of doc its $ref names, as many times as one names another.
func followDefs(t require.TestingT, doc *openapi.Document, schema any) any {
	for range len(doc.Defs) + 1 {
		m, _ := schema.(map[string]any) // or a boolean schema
		ref, ok := m["$ref"].(string)
		if !ok {
			return schema
		}

		name, ok := strings.CutPrefix(ref, "#/$defs/")
		require.True(t, ok, ref)

		schema = doc.Defs[name]
	}

	require.Fail(t, "a cycle of $defs")

	return nil
}

// normal returns v as a spec holds it: written as JSON and read back.
func normal(t require.TestingT, v any) any {
	data, err := json.Marshal(v, json.Deterministic(true))
	require.NoError(t, err)

	n, err := value.DecodeJSON(data)
	require.NoError(t, err)

	return n
}

// setPointer sets the value of doc at ptr, a JSON pointer of objects, making the objects on the way.
func setPointer(doc map[string]any, ptr string, v any) {
	tokens := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for _, token := range tokens[:len(tokens)-1] {
		next, ok := doc[token].(map[string]any)
		if !ok {
			next = make(map[string]any)
			doc[token] = next
		}

		doc = next
	}

	doc[tokens[len(tokens)-1]] = v
}

// drawnSpec is a spec a property draws: its files, by path from its folder, spec.yaml its root, each written as
// JSON, which is YAML too.
type drawnSpec struct {
	files map[string]map[string]any
	n     int // the objects placed so far, which number their places
}

// place is where an object of a spec sits.
type place struct {
	file    string
	pointer string // "" for the whole file
}

func newDrawnSpec() *drawnSpec {
	return &drawnSpec{files: map[string]map[string]any{"spec.yaml": {
		"openapi": "3.1.0", "info": map[string]any{"title": "Refs", "version": "1"}, "paths": make(map[string]any),
	}}}
}

func (s *drawnSpec) paths() map[string]any {
	return s.files["spec.yaml"]["paths"].(map[string]any)
}

func (s *drawnSpec) components(collection string) map[string]any {
	root := s.files["spec.yaml"]
	if root["components"] == nil {
		root["components"] = make(map[string]any)
	}

	c := root["components"].(map[string]any)
	if c[collection] == nil {
		c[collection] = make(map[string]any)
	}

	return c[collection].(map[string]any)
}

// draw draws the places an object of collection sits, the first holding it, each next one a $ref to the one before:
// none for an object written inline. Only the first is ever a whole file: kin-openapi rejects a file that is only a
// $ref.
func (s *drawnSpec) draw(t *rapid.T, collection string) []place {
	wheres := []string{"component", "file", "sub folder"}
	if collection == "parameters" {
		wheres = append(wheres, "another path")
	}

	hops := rapid.IntRange(0, 3).Draw(t, collection+" hops")
	places := make([]place, hops)

	for i := range places {
		s.n++

		where := wheres
		if i == 0 {
			where = append(slices.Clone(wheres), "whole file")
		}

		switch rapid.SampledFrom(where).Draw(t, collection+" where") {
		case "component":
			places[i] = place{file: "spec.yaml", pointer: fmt.Sprintf("/components/%s/C%d", collection, s.n)}
		case "file":
			places[i] = place{file: fmt.Sprintf("f%d.yaml", s.n), pointer: "/O"}
		case "sub folder":
			places[i] = place{file: fmt.Sprintf("sub/f%d.yaml", s.n), pointer: "/O"}
		case "whole file":
			places[i] = place{file: fmt.Sprintf("w%d.yaml", s.n)}
		case "another path":
			places[i] = place{file: "spec.yaml", pointer: fmt.Sprintf("/paths/~1p%d~1{id}/get/parameters/0", s.n)}
		}
	}

	return places
}

// holder returns the file that holds an object at places, written in from when inline.
func (s *drawnSpec) holder(places []place, from string) string {
	if len(places) == 0 {
		return from
	}

	return places[0].file
}

// put writes obj at places, each next one a $ref to the one before, and returns what refers to it from the file
// from: obj, when inline.
func (s *drawnSpec) put(places []place, from string, obj map[string]any) any {
	var v any = obj

	for i, p := range places {
		if i > 0 {
			v = map[string]any{"$ref": relRef(p.file, places[i-1])}
		}

		switch {
		case p.pointer == "":
			s.files[p.file] = v.(map[string]any)
		case strings.HasPrefix(p.pointer, "/paths/"):
			path := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(p.pointer, "/paths/"), "/get/parameters/0"), "~1", "/")
			s.paths()[path] = map[string]any{"get": map[string]any{"parameters": []any{v}, "responses": map[string]any{"204": map[string]any{"description": "OK"}}}}
		default:
			if s.files[p.file] == nil {
				s.files[p.file] = make(map[string]any)
			}

			setPointer(s.files[p.file], p.pointer, v)
		}
	}

	if len(places) > 0 {
		v = map[string]any{"$ref": relRef(from, places[len(places)-1])}
	}

	return v
}

// beside places a schema, empty or an allOf of the shared schema Empty, and returns what refers to it from the file
// from, with const c: beside its $ref, when it has one.
func (s *drawnSpec) beside(t *rapid.T, from string, c any) map[string]any {
	at := s.draw(t, "schemas")

	target := make(map[string]any)
	if rapid.Bool().Draw(t, "allOf") {
		empty := place{file: "spec.yaml", pointer: "/components/schemas/Empty"}
		target["allOf"] = []any{map[string]any{"$ref": relRef(s.holder(at, from), empty)}}
	}

	m := s.put(at, from, target).(map[string]any)
	m["const"] = c

	return m
}

// besideConst returns the const of schema, which beside wrote: beside its $ref, or, where kin-openapi resolved the
// $ref, its last allOf entry, after the target's own.
func besideConst(t require.TestingT, schema any) any {
	m, _ := schema.(map[string]any)
	if c, ok := m["const"]; ok {
		return c
	}

	all, _ := m["allOf"].([]any)
	require.NotEmpty(t, all, "%v", schema)

	for _, own := range all[:len(all)-1] {
		require.Equal(t, map[string]any{"$ref": "#/$defs/Empty"}, own, "%v", schema)
	}

	c, ok := all[len(all)-1].(map[string]any)["const"]
	require.True(t, ok, "%v", schema)

	return c
}

// relRef returns the $ref the file from writes for p.
func relRef(from string, p place) string {
	ref := "#" + p.pointer
	if p.file != from {
		rel, _ := filepath.Rel(filepath.Dir(filepath.FromSlash(from)), filepath.FromSlash(p.file))
		ref = filepath.ToSlash(rel) + strings.TrimSuffix(ref, "#")
	}

	return ref
}

// load writes the files of s to a folder of t's and loads its spec.yaml.
func (s *drawnSpec) load(t *testing.T, rt *rapid.T) *openapi.Document {
	dir := t.TempDir()

	for name, content := range s.files {
		data, err := json.Marshal(content, json.Deterministic(true))
		require.NoError(rt, err)

		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(rt, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(rt, os.WriteFile(path, data, 0o600))
	}

	loc, err := openapi.Location(filepath.Join(dir, "spec.yaml"))
	require.NoError(rt, err)

	data, err := read(t)(loc)
	require.NoError(rt, err)

	log := zerolog.Nop()
	doc, err := openapiv3.Load(t.Context(), data, loc, read(t), &log)
	require.NoError(rt, err)

	return doc
}

// Generators

// genJSON draws a JSON value, often an integer past 2^53, which a float64 rounds, or a null.
func genJSON(depth int) *rapid.Generator[any] {
	scalars := rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.String(), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(rapid.OneOf(rapid.Int64Range(1<<53, math.MaxInt64), rapid.Int64Range(math.MinInt64, -(1<<53))), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-1e300, 1e300), func(f float64) any { return f }),
	)

	if depth == 0 {
		return scalars
	}

	return rapid.OneOf(
		scalars,
		rapid.Map(rapid.SliceOfN(genJSON(depth-1), 0, 3), func(l []any) any { return l }),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-c]`), genJSON(depth-1), 0, 3), func(m map[string]any) any { return m }),
	)
}
