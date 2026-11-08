package openapiv3_test

import (
	"cmp"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

// fitCase is an example of a spec of one operation, GET /orders/{id}, whose path item gives id a schema and no
// example, what the model holds of it, and the warning it is logged with, if any.
type fitCase struct {
	name       string
	version    string                          // the spec's OpenAPI version, 3.1.0 when not set
	operation  string                          // the operation, its parameters and responses
	components string                          // the spec's components, if any
	got        func(doc *openapi.Document) any // what the model holds of the example
	want       any
	warning    map[string]any
}

func TestLoad_ConvertsAnExampleToTheTypeOfItsSchema(t *testing.T) {
	// Specs write a string's example as a number, or a number's as a string, as often as not: such an example still
	// names the value the spec means. Taken as written, it would make a mock that breaks the contract; refused, the
	// whole spec would make no mocks.
	tests := []fitCase{
		{
			name:      "a path parameter",
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: string}, example: 12345}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example{{Value: "12345"}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/example",
				"message": `the example does not fit its schema: converted 12345 to "12345"`,
			},
		},
		{
			name: "a body",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {" +
				"schema: {type: object, properties: {number: {type: string}, total: {type: number}, paid: {type: boolean}}}, " +
				"example: {number: 10203040, total: \"12.5\", paid: \"true\", other: 1}}}}}",
			got:  func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want: []openapi.Example{{Value: map[string]any{"number": "10203040", "total": 12.5, "paid": true, "other": int64(1)}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": `the example does not fit its schema: converted 10203040 at /number to "10203040", "true" at /paid to true, "12.5" at /total to 12.5`,
			},
		},
		{
			name:       "a named example of the components",
			operation:  "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: array, items: {type: integer}}, examples: {first: {$ref: \"#/components/examples/IDs\"}}}}}}",
			components: "examples: {IDs: {value: [\"1\", 2]}}",
			got:        func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want:       []openapi.Example{{Name: "first", Value: []any{int64(1), int64(2)}}},
			warning: map[string]any{
				"example": "spec.yaml#/components/examples/IDs/value",
				"message": `the example does not fit its schema: converted "1" at /0 to 1`,
			},
		},
		{
			name: "a body of all of several schemas",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {" +
				"schema: {allOf: [{$ref: \"#/components/schemas/Base\"}, {properties: {b: {type: string}}}]}, example: {a: \"1\", b: 2}}}}}",
			components: "schemas: {Base: {properties: {a: {type: integer}}}}",
			got:        func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want:       []openapi.Example{{Value: map[string]any{"a": int64(1), "b": "2"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": `the example does not fit its schema: converted "1" at /a to 1, 2 at /b to "2"`,
			},
		},
		{
			name:      "a body of a map",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {additionalProperties: {type: string}}, example: {x/y: 1}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want:      []openapi.Example{{Value: map[string]any{"x/y": "1"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": `the example does not fit its schema: converted 1 at /x~1y to "1"`,
			},
		},
		{
			name: "a body whose numbers JSON would write otherwise",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {" +
				"schema: {properties: {zip: {type: string}, version: {type: string}}}, example: {zip: 01234, version: 20.10}}}}}",
			got:  func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want: []openapi.Example{{Value: map[string]any{"zip": "01234", "version": "20.10"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": `the example does not fit its schema: converted 20.10 at /version to "20.10", 01234 at /zip to "01234"`,
			},
		},
		{
			name: "a body a YAML alias and merge key write",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {" +
				"schema: {properties: {n: {type: string}, k: {type: string}, j: {type: string}, a: {properties: {m: {type: string}}}}}, " +
				"example: {<<: {n: 5, a: {m: 6}}, k: &seven 7.0, j: *seven}}}}}",
			got:  func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want: []openapi.Example{{Value: map[string]any{"a": map[string]any{"m": "6"}, "j": "7.0", "k": "7.0", "n": "5"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": `the example does not fit its schema: converted 6 at /a/m to "6", 7.0 at /j to "7.0", 7.0 at /k to "7.0", 5 at /n to "5"`,
			},
		},
		{
			name:      "a header",
			operation: "responses: {\"204\": {description: OK, headers: {X-Rate: {schema: {type: integer}, example: \"5\"}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Headers[0].Examples },
			want:      []openapi.Example{{Value: int64(5)}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/204/headers/X-Rate/example",
				"message": `the example does not fit its schema: converted "5" to 5`,
			},
		},
		{
			name:       "a property of a shared schema",
			operation:  "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/Order\"}}}}}",
			components: "schemas: {Order: {type: object, properties: {number: {type: string, example: 10203040}}}}",
			got: func(doc *openapi.Document) any {
				return doc.Defs["Order"].(map[string]any)["properties"].(map[string]any)["number"]
			},
			want: map[string]any{"type": "string", "example": "10203040"},
			warning: map[string]any{
				"example": "spec.yaml#/components/schemas/Order/properties/number/example",
				"message": `the example does not fit its schema: converted 10203040 to "10203040"`,
			},
		},
		{
			name:       "an example beside a $ref",
			operation:  "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/ID\", example: 7}}}}}",
			components: "schemas: {ID: {type: string, example: \"5\"}}",
			got:        func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:       map[string]any{"$ref": "#/$defs/ID", "example": "7"},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/example",
				"message": `the example does not fit its schema: converted 7 to "7"`,
			},
		},
		{
			name:      "a branch of a body's schema",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {oneOf: [{type: integer}, {type: string, example: 1}]}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:      map[string]any{"oneOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "example": "1"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/oneOf/1/example",
				"message": `the example does not fit its schema: converted 1 to "1"`,
			},
		},
		{
			name:      "a property of a body's schema",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: array, items: {type: boolean, example: \"false\"}}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:      map[string]any{"type": "array", "items": map[string]any{"type": "boolean", "example": false}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/items/example",
				"message": `the example does not fit its schema: converted "false" to false`,
			},
		},
	}

	for _, version := range []string{"3.0.3", "3.1.0"} {
		for _, tt := range tests {
			tt.version = version
			t.Run(version+": "+tt.name, func(t *testing.T) { tt.check(t) })
		}
	}

	t.Run("the dataValue of an example of OpenAPI 3.2", func(t *testing.T) {
		fitCase{
			version:   "3.2.0",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: string}, examples: {a: {dataValue: 5}}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want:      []openapi.Example{{Name: "a", Value: "5"}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/examples/a/dataValue",
				"message": `the example does not fit its schema: converted 5 to "5"`,
			},
		}.check(t)
	})

	t.Run("the examples of a body's schema", func(t *testing.T) {
		fitCase{
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: string, examples: [a, 2.50]}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:      map[string]any{"type": "string", "examples": []any{"a", "2.50"}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/examples/1",
				"message": `the example does not fit its schema: converted 2.50 to "2.50"`,
			},
		}.check(t)
	})

	t.Run("a number for the one type of several it reads as", func(t *testing.T) {
		fitCase{
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: [boolean, string]}, example: 12345}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example{{Value: "12345"}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/example",
				"message": `the example does not fit its schema: converted 12345 to "12345"`,
			},
		}.check(t)
	})

	t.Run("the examples of a schema", func(t *testing.T) {
		fitCase{
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: string, examples: [a, 7]}}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example{{Value: "a"}, {Value: "7"}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/schema/examples/1",
				"message": `the example does not fit its schema: converted 7 to "7"`,
			},
		}.check(t)
	})
}

func TestLoad_LeavesOutAnExampleNoConversionFits(t *testing.T) {
	// An example that breaks its schema otherwise than by its type names no value the spec means: the mocks take the
	// next example, or a fake value of the schema, and the log says which example to fix.
	tests := []fitCase{
		{
			name: "a body",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: object, properties: {n: {type: integer, minimum: 3}}}, " +
				"examples: {a: {value: {n: 1}}, b: {value: {n: 3}}}}}}}",
			got:  func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want: []openapi.Example{{Name: "b", Value: map[string]any{"n": int64(3)}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/examples/a/value",
				"message": "the example breaks its schema's minimum at /n: left out",
			},
		},
		{
			name:      "a path parameter",
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: integer}, example: abc}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/example",
				"message": "the example breaks its schema's type: left out",
			},
		},
		{
			name:      "an example converted that still breaks its schema",
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: integer, minimum: 10}, example: \"5\"}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/example",
				"message": "the example breaks its schema's minimum: left out",
			},
		},
		{
			name:      "a list its schema gives no items for",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: array, maxItems: 1}, example: [1, 2]}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/example",
				"message": "the example breaks its schema's maxItems: left out",
			},
		},
		{
			name:      "a number written with spaces",
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: integer}, example: \" 12\"}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/example",
				"message": "the example breaks its schema's type: left out",
			},
		},
		{
			name:      "every example of a body's schema",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {type: integer, examples: [a]}}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:      map[string]any{"type": "integer"},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/examples/0",
				"message": "the example breaks its schema's type: left out",
			},
		},
		{
			name:       "a property of a shared schema",
			operation:  "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/Order\"}}}}}",
			components: "schemas: {Order: {type: object, properties: {number: {type: string, pattern: \"^[0-9]+$\", example: A1}}}}",
			got: func(doc *openapi.Document) any {
				return doc.Defs["Order"].(map[string]any)["properties"].(map[string]any)["number"]
			},
			want: map[string]any{"type": "string", "pattern": "^[0-9]+$"},
			warning: map[string]any{
				"example": "spec.yaml#/components/schemas/Order/properties/number/example",
				"message": "the example breaks its schema's pattern: left out",
			},
		},
		{
			name:      "a null",
			operation: "responses: {\"204\": {description: OK, headers: {X-Rate: {schema: {type: integer}, example: null}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Headers[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/204/headers/X-Rate/example",
				"message": "the example breaks its schema's type: left out",
			},
		},
		{
			name:      "a null in OpenAPI 3.0",
			version:   "3.0.3",
			operation: "responses: {\"204\": {description: OK, headers: {X-Rate: {schema: {type: integer}, example: null}}}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Headers[0].Examples },
			want:      []openapi.Example(nil),
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/204/headers/X-Rate/example",
				"message": "the example breaks its schema's nullable: left out",
			},
		},
		{
			name: "a null beside a $ref",
			operation: "responses: {\"200\": {description: OK, content: {application/json: {schema: {$ref: \"#/components/schemas/Any\", " +
				"properties: {a: {type: object, example: null}}}}}}}",
			components: "schemas: {Any: {}}",
			got:        func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Schema },
			want:       map[string]any{"$ref": "#/$defs/Any", "properties": map[string]any{"a": map[string]any{"type": "object"}}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/properties/a/example",
				"message": "the example breaks its schema's type: left out",
			},
		},
		{
			name:      "an example of a schema's examples",
			operation: "parameters: [{name: id, in: path, required: true, schema: {type: integer, examples: [abc, 7]}}]\n      responses: {\"204\": {description: OK}}",
			got:       func(doc *openapi.Document) any { return doc.Operations[0].PathParams[0].Examples },
			want:      []openapi.Example{{Value: int64(7)}},
			warning: map[string]any{
				"example": "spec.yaml#/paths/~1orders~1{id}/get/parameters/0/schema/examples/0",
				"message": "the example breaks its schema's type: left out",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t) })
	}
}

func TestLoad_TakesAnExampleOutsideJSONAsTheBodyItself(t *testing.T) {
	// The example of a media type that isn't JSON is the body as sent, such as XML text, not the data its schema
	// describes: no schema rejects it, nor converts it.
	fitCase{
		operation: "responses: {\"200\": {description: OK, content: {text/plain: {schema: {type: integer}, example: \"12\"}}}}",
		got:       func(doc *openapi.Document) any { return doc.Operations[0].Responses[0].Content[0].Examples },
		want:      []openapi.Example{{Value: "12"}},
	}.check(t)
}

func TestLoad_ChecksNoSchemaBesideARefInOpenAPI30(t *testing.T) {
	// OpenAPI 3.1 reads the schemas beside a $ref as one more allOf entry of what it names, its last; OpenAPI 3.0
	// reads them as no part of the schema, so the last allOf entry of what it names is no schema of theirs.
	doc := load(t, map[string]string{"spec.yaml": `
openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    get:
      responses: {"200": {description: OK, content: {application/json: {schema: {$ref: "#/components/schemas/Order", properties: {a: {example: x}}}}}}}
components:
  schemas:
    Order: {allOf: [{properties: {a: {type: integer}}}]}
`})

	require.Equal(t, map[string]any{"$ref": "#/$defs/Order", "properties": map[string]any{"a": map[string]any{"example": "x"}}}, doc.Operations[0].Responses[0].Content[0].Schema)
}

func TestLoad_WarnsOnceOfAnExampleItsOperationsShare(t *testing.T) {
	// A response the operations share is fixed in one place.
	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Orders, version: "1"}
paths:
  /a: {get: {responses: {"200": {$ref: "#/components/responses/Order"}}}}
  /b: {get: {responses: {"200": {$ref: "#/components/responses/Order"}}}}
components:
  responses:
    Order: {description: OK, content: {application/json: {schema: {type: string}, example: 1}}}
`})

	for _, op := range doc.Operations {
		require.Equal(t, []openapi.Example{{Value: "1"}}, op.Responses[0].Content[0].Examples, op.Path)
	}

	require.Equal(t, []map[string]any{{
		"level":   "warn",
		"example": "spec.yaml#/components/responses/Order/content/application~1json/example",
		"message": `the example does not fit its schema: converted 1 to "1"`,
	}}, warnings)
}

func TestLoad_ConvertsAnExampleForEachSchemaThatUsesIt(t *testing.T) {
	// A named example of the components may serve schemas that ask different types of it: each gets the example as
	// its schema asks, and none the other's.
	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Orders, version: "1"}
paths:
  /a: {get: {responses: {"200": {description: OK, content: {application/json: {schema: {items: {type: string}}, examples: {ids: {$ref: "#/components/examples/IDs"}}}}}}}}
  /b: {get: {responses: {"200": {description: OK, content: {application/json: {schema: {items: {type: integer}}, examples: {ids: {$ref: "#/components/examples/IDs"}}}}}}}}
components:
  examples:
    IDs: {value: [1, 2]}
`})

	require.Equal(t, []openapi.Example{{Name: "ids", Value: []any{"1", "2"}}}, operation(t, doc, "/a").Responses[0].Content[0].Examples)
	require.Equal(t, []openapi.Example{{Name: "ids", Value: []any{int64(1), int64(2)}}}, operation(t, doc, "/b").Responses[0].Content[0].Examples)
	require.Equal(t, []map[string]any{{
		"level":   "warn",
		"example": "spec.yaml#/components/examples/IDs/value",
		"message": `the example does not fit its schema: converted 1 at /0 to "1", 2 at /1 to "2"`,
	}}, warnings)
}

func TestLoad_WarnsOfTheExamplesInTheSameOrderOnEveryRun(t *testing.T) {
	// The log of one spec reads the same on every run, so a rerun shows what changed.
	files := map[string]string{"spec.yaml": `
openapi: 3.1.0
info: {title: Orders, version: "1"}
paths:
  /a/{id}:
    get:
      parameters: [{name: id, in: path, required: true, schema: {type: string}, example: 1}]
      responses: {"200": {description: OK, content: {application/json: {schema: {properties: {a: {type: string, example: 2}, b: {type: string, example: 3}}}}}}}
components:
  schemas:
    A: {type: string, example: 4}
    B: {type: string, example: 5}
    C: {type: string, example: 6}
`}

	_, first := loadWithWarnings(t, files)
	require.Len(t, first, 6)

	for range 10 {
		_, warnings := loadWithWarnings(t, files)
		require.Equal(t, first, warnings)
	}
}

// check loads the spec of c and checks what the model holds of its example, and what it logs.
func (c fitCase) check(t *testing.T) {
	t.Helper()

	spec := "openapi: " + cmp.Or(c.version, "3.1.0") + "\ninfo: {title: Orders, version: \"1\"}\npaths:\n  /orders/{id}:\n" +
		"    parameters: [{name: id, in: path, required: true, schema: {type: string}}]\n    get:\n      " + c.operation + "\n"
	if c.components != "" {
		spec += "components: {" + c.components + "}\n"
	}

	doc, warnings := loadWithWarnings(t, map[string]string{"spec.yaml": spec})
	require.Equal(t, c.want, c.got(doc))

	var want []map[string]any
	if c.warning != nil {
		w := map[string]any{"level": "warn"}
		maps.Copy(w, c.warning)
		want = append(want, w)
	}

	require.Equal(t, want, warnings)
}
