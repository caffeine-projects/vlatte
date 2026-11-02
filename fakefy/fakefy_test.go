package fakefy_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/fakefy"
)

const _seeds = 50

func TestSchema_ValuesValidateAgainstTheirSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema string
	}{
		{name: "object with required and optional properties", schema: `{
			"type": "object", "required": ["id", "name"],
			"properties": {
				"id": {"type": "string", "format": "uuid"},
				"name": {"type": "string", "minLength": 2, "maxLength": 20},
				"age": {"type": "integer", "minimum": 0, "maximum": 30},
				"tags": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 4, "uniqueItems": true}
			},
			"additionalProperties": false}`},
		{name: "bounds that exclude their limits", schema: `{
			"type": "object", "required": ["i", "n"],
			"properties": {
				"i": {"type": "integer", "exclusiveMinimum": 1, "exclusiveMaximum": 3},
				"n": {"type": "number", "exclusiveMinimum": 0, "maximum": 0.5}
			}}`},
		{name: "bounds narrower than two decimals", schema: `{"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 0.004}`},
		{name: "multiples", schema: `{
			"type": "object", "required": ["i", "n"],
			"properties": {
				"i": {"type": "integer", "minimum": 7, "maximum": 100, "multipleOf": 5},
				"n": {"type": "number", "minimum": 0, "maximum": 1, "multipleOf": 0.1}
			}}`},
		{name: "a single bound", schema: `{
			"type": "object", "required": ["lo", "hi"],
			"properties": {"lo": {"type": "integer", "minimum": 1000}, "hi": {"type": "number", "maximum": -50}}}`},
		{name: "refs within the document", schema: `{
			"$defs": {"Owner": {"type": "object", "required": ["email"], "properties": {"email": {"type": "string", "format": "email"}}}},
			"type": "object", "required": ["owner"], "properties": {"owner": {"$ref": "#/$defs/Owner"}}}`},
		{name: "a recursive schema", schema: `{
			"$defs": {"Node": {"type": "object", "required": ["name"], "properties": {
				"name": {"type": "string"}, "children": {"type": "array", "items": {"$ref": "#/$defs/Node"}}}}},
			"$ref": "#/$defs/Node"}`},
		{name: "allOf", schema: `{"allOf": [
			{"type": "object", "required": ["a"], "properties": {"a": {"type": "integer", "minimum": 5}}},
			{"required": ["b"], "properties": {"a": {"maximum": 6}, "b": {"type": "boolean"}}}]}`},
		{name: "anyOf of consts", schema: `{"anyOf": [{"const": "DOG"}, {"const": "CAT"}, {"const": "BIRD"}]}`},
		{name: "oneOf of different types", schema: `{"oneOf": [{"type": "string", "maxLength": 3}, {"type": "integer"}, {"type": "null"}]}`},
		{name: "a nullable type list", schema: `{"type": ["null", "string"], "format": "date"}`},
		{name: "min and max properties", schema: `{"type": "object", "minProperties": 2, "maxProperties": 3, "additionalProperties": {"type": "integer"}}`},
		{name: "prefix items", schema: `{"type": "array", "prefixItems": [{"type": "integer"}, {"type": "string", "format": "email"}], "minItems": 2, "maxItems": 2}`},
		{name: "a true schema", schema: `{"type": "object", "required": ["x"], "properties": {"x": true}}`},
		{name: "keywords next to a $ref", schema: `{
			"$defs": {"A": {"type": "object", "required": ["a"], "properties": {"a": {"type": "integer"}}}},
			"$ref": "#/$defs/A", "required": ["b"], "properties": {"b": {"type": "integer"}}}`},
		{name: "a minimum next to a looser exclusiveMinimum", schema: `{"type": "integer", "minimum": 10, "exclusiveMinimum": 0, "maximum": 20}`},
		{name: "an exclusiveMaximum next to a looser maximum", schema: `{"type": "number", "minimum": 0, "maximum": 100, "exclusiveMaximum": 1}`},
		{name: "allOf bounds of both forms", schema: `{"allOf": [{"type": "integer", "minimum": 10}, {"exclusiveMinimum": 0, "maximum": 12}]}`},
		{name: "allOf multiples", schema: `{"allOf": [{"type": "integer", "minimum": 0, "maximum": 100, "multipleOf": 2}, {"multipleOf": 3}]}`},
		{name: "allOf fraction multiples", schema: `{"allOf": [{"type": "number", "minimum": 0, "maximum": 10, "multipleOf": 0.5}, {"multipleOf": 0.3}]}`},
		{name: "allOf number and integer", schema: `{"allOf": [{"type": "number"}, {"type": "integer"}]}`},
		{name: "allOf type lists", schema: `{"allOf": [{"type": ["string", "null"]}, {"type": ["integer", "null"]}]}`},
		{name: "allOf enums", schema: `{"allOf": [{"enum": ["a", "b"]}, {"enum": ["b", "c"]}]}`},
		{name: "allOf items", schema: `{"type": "array", "minItems": 1, "allOf": [{"items": {"type": "integer"}}, {"items": {"minimum": 5, "maximum": 9}}]}`},
		{name: "allOf patterns", schema: `{"allOf": [{"type": "string", "pattern": "^[a-z]+$"}, {"pattern": "^.{3}$"}]}`},
		{name: "allOf additionalProperties", schema: `{"type": "object", "minProperties": 1, "allOf": [{"additionalProperties": {"type": "integer"}}, {"additionalProperties": {"minimum": 3, "maximum": 4}}]}`},
		{name: "multiples of a fraction far from zero", schema: `{"type": "number", "minimum": 1e9, "maximum": 1000000001, "multipleOf": 0.003}`},
		{name: "an integer range wider than int64 holds", schema: `{"type": "integer", "minimum": -5e18, "maximum": 5e18}`},
		{name: "a number multiple over a wide range", schema: `{"type": "number", "minimum": -1e300, "maximum": 1e300, "multipleOf": 0.00001}`},
		{name: "an integer multiple of a fraction", schema: `{"type": "integer", "multipleOf": 2.5}`},
		{name: "an integer multiple above the default range", schema: `{"type": "integer", "multipleOf": 1000}`},
		{name: "a number multiple below a maximum", schema: `{"type": "number", "maximum": -5000, "multipleOf": 1000}`},
		{name: "unique items of an example", schema: `{"type": "array", "uniqueItems": true, "items": {"type": "string", "example": "x"}}`},
		{name: "unique items of a small enum", schema: `{"type": "array", "uniqueItems": true, "maxItems": 3, "items": {"enum": ["a", "b"]}}`},
		{name: "unique items of numbers equal as JSON", schema: `{"type": "array", "uniqueItems": true, "minItems": 1, "items": {"enum": [1, 1.0]}}`},
		{name: "prefix items and no others", schema: `{"type": "array", "prefixItems": [{"type": "integer"}], "items": false}`},
		{name: "a number range of a double", schema: `{"type": "number", "minimum": -1.7976931348623157e308, "maximum": 1.7976931348623157e308}`},
		{name: "a string as long as a length allows", schema: `{"type": "string", "maxLength": 9223372036854775807}`},
		{name: "an example the allOf breaks", schema: `{
			"$defs": {"Pet": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}, "example": {"name": "x"}}},
			"allOf": [{"$ref": "#/$defs/Pet"}, {"required": ["bark"], "properties": {"bark": {"type": "boolean"}}}]}`},
		{name: "an enum value of another type", schema: `{"type": "string", "enum": ["a", "b", null]}`},
		{name: "allOf of a closed object and another property", schema: `{"allOf": [
			{"type": "object", "properties": {"a": {"type": "string"}}, "additionalProperties": false},
			{"properties": {"b": {"type": "string"}}}]}`},
		{name: "allOf of oneOfs some branches of which conflict", schema: `{"allOf": [
			{"oneOf": [{"type": "string"}, {"type": "integer"}]}, {"oneOf": [{"type": "string"}, {"type": "boolean"}]}]}`},
		{name: "a property no value satisfies", schema: `{"type": "object", "properties": {"secret": false, "name": {"type": "string"}}}`},
		{name: "optional items no value satisfies", schema: `{"type": "array", "items": false}`},
		{name: "a property a pattern constrains too", schema: `{"type": "object", "required": ["x1"], "properties": {"x1": {"type": "string"}}, "patternProperties": {"^x": {"maxLength": 2}}}`},
		{name: "a required property of a pattern", schema: `{"type": "object", "required": ["x1"], "patternProperties": {"^x": {"type": "integer"}}, "additionalProperties": false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch, doc := compile(t, tt.schema)

			for seed := range uint64(_seeds) {
				v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
				require.NoError(t, err)
				requireValid(t, sch, v)
			}
		})
	}
}

func TestSchema_TheFirstKeywordThatGivesAValueWins(t *testing.T) {
	// A spec's examples say what a field holds better than a value made up from its type, so they win, in this order.
	tests := []struct {
		name   string
		schema map[string]any
		want   any
	}{
		{name: "const", schema: map[string]any{"const": "c", "examples": []any{"x"}, "example": "e", "default": "d"}, want: "c"},
		{name: "examples", schema: map[string]any{"examples": []any{"x", "y"}, "example": "e", "default": "d", "type": "string"}, want: "x"},
		{name: "example", schema: map[string]any{"example": "e", "default": "d", "type": "string"}, want: "e"},
		{name: "default", schema: map[string]any{"default": "d", "type": "string"}, want: "d"},
		{name: "enum", schema: map[string]any{"enum": []any{"n"}, "type": "string"}, want: "n"},
		{name: "a null const", schema: map[string]any{"const": nil, "type": []any{"string", "null"}}, want: nil},
		{name: "property examples inside a generated object", schema: map[string]any{
			"type": "object", "required": []any{"name"},
			"properties": map[string]any{"name": map[string]any{"type": "string", "example": "Buddy"}},
		}, want: map[string]any{"name": "Buddy"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := fakefy.New(fakefy.WithSeed(1)).Schema(tt.schema)
			require.NoError(t, err)
			require.Equal(t, tt.want, v)
		})
	}
}

func TestWithSeed_MakesTheSameValuesOnEveryRun(t *testing.T) {
	// Generated mock files are committed: a rerun on the same spec must write the same bytes.
	_, doc := compile(t, `{"type": "object", "properties": {
		"id": {"type": "string", "format": "uuid"}, "at": {"type": "string", "format": "date-time"},
		"n": {"type": "number"}, "tags": {"type": "array", "items": {"type": "string"}}}}`)

	a, b, c := fakefy.New(fakefy.WithSeed(7)), fakefy.New(fakefy.WithSeed(7)), fakefy.New(fakefy.WithSeed(8))

	for range 5 {
		va, err := a.Schema(doc)
		require.NoError(t, err)

		vb, err := b.Schema(doc)
		require.NoError(t, err)

		vc, err := c.Schema(doc)
		require.NoError(t, err)

		require.Equal(t, va, vb)
		require.NotEqual(t, va, vc)
	}
}

func TestSchema_FailsWithErrUnsatisfiableWhenNoValueFits(t *testing.T) {
	// The error names the place the generator gave up at and why, so a spec's author can find the schema to fix.
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{name: "minimum above maximum", schema: `{"type": "object", "required": ["age"], "properties": {"age": {"type": "integer", "minimum": 5, "maximum": 1}}}`, want: `at "/age": minimum 5 is above maximum 1`},
		{name: "minLength above maxLength", schema: `{"type": "string", "minLength": 4, "maxLength": 2}`, want: "at the root: minLength 4 is above maxLength 2"},
		{name: "minItems above maxItems", schema: `{"type": "array", "minItems": 3, "maxItems": 1}`, want: "at the root: minItems 3 is above maxItems 1"},
		{name: "a false schema", schema: `{"type": "object", "required": ["x"], "properties": {"x": false}}`, want: `at "/x": the schema is false`},
		{name: "no integer between exclusive bounds", schema: `{"type": "integer", "exclusiveMinimum": 1, "exclusiveMaximum": 2}`, want: "at the root: no integer between 1 and 2"},
		{name: "no multiple within the bounds", schema: `{"type": "integer", "minimum": 1, "maximum": 4, "multipleOf": 5}`, want: "at the root: no multiple of 5 between 1 and 4"},
		{name: "a required property not allowed", schema: `{"type": "object", "required": ["x"], "additionalProperties": false}`, want: `at the root: required property "x" is not allowed`},
		{name: "required properties without end", schema: `{"$defs": {"N": {"type": "object", "required": ["next"], "properties": {"next": {"$ref": "#/$defs/N"}}}}, "$ref": "#/$defs/N"}`, want: `at "` + strings.Repeat("/next", 33) + `": the value nests deeper than 32 levels`},
		{name: "an allOf that names itself", schema: `{"$defs": {"A": {"allOf": [{"$ref": "#/$defs/A"}]}}, "$ref": "#/$defs/A"}`, want: "at the root: allOf and $ref nest deeper than 32 schemas"},
		{name: "unique items from too few values", schema: `{"type": "array", "minItems": 3, "uniqueItems": true, "items": {"type": "boolean"}}`, want: "at the root: expected 3 unique items, found 2"},
		{name: "prefix items too few for minItems", schema: `{"type": "array", "minItems": 2, "prefixItems": [{"type": "integer"}], "items": false}`, want: "at the root: minItems 2 is above the 1 prefixItems allowed"},
		{name: "integers past int64", schema: `{"type": "integer", "minimum": 1e19}`, want: "at the root: integers from 1e+19 to 1e+19 don't fit an int64"},
		{name: "a format longer than maxLength", schema: `{"type": "string", "format": "date-time", "maxLength": 10}`, want: `at the root: format "date-time" makes no string of 0 to 10 characters`},
		{name: "a format shorter than minLength", schema: `{"type": "string", "format": "uuid", "minLength": 40}`, want: `at the root: format "uuid" makes no string of at least 40 characters`},
		{name: "minProperties above maxProperties", schema: `{"type": "object", "minProperties": 3, "maxProperties": 1}`, want: "at the root: minProperties 3 is above maxProperties 1"},
		{name: "allOf of types that share no value", schema: `{"allOf": [{"type": "string"}, {"type": "integer"}]}`, want: `at the root: allOf: no type is both of ["string"] and ["integer"]`},
		{name: "allOf of enums that share no value", schema: `{"allOf": [{"enum": ["a"]}, {"enum": ["b"]}]}`, want: "at the root: allOf: no value is in both enums"},
		{name: "allOf of different consts", schema: `{"allOf": [{"const": 1}, {"const": 2}]}`, want: "at the root: allOf: two consts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, doc := compile(t, tt.schema)

			_, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
			require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
			require.EqualError(t, err, "fakefy: no value satisfies the schema: "+tt.want)
		})
	}
}

func TestSchema_FailsWithErrInvalidForWhatIsNotASchema(t *testing.T) {
	// A spec that isn't valid is the author's to fix, not a value the generator can't find: the errors tell them apart.
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{name: "a number as a schema", schema: map[string]any{"properties": map[string]any{"a": 3}}, want: `at "/a": expected a schema object or boolean, not int`},
		{name: "a negative minLength", schema: map[string]any{"type": "string", "minLength": -2, "maxLength": -1}, want: "at the root: minLength must be a whole number from 0, not -2"},
		{name: "a negative minItems", schema: map[string]any{"type": "array", "minItems": -5}, want: "at the root: minItems must be a whole number from 0, not -5"},
		{name: "a maxProperties that isn't whole", schema: map[string]any{"type": "object", "maxProperties": 1.5}, want: "at the root: maxProperties must be a whole number from 0, not 1.5"},
		{name: "a pattern that doesn't compile", schema: map[string]any{"type": "string", "pattern": "(unclosed"}, want: "at the root: pattern \"(unclosed\": compiling: error parsing regexp: missing closing ): `(unclosed`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fakefy.New(fakefy.WithSeed(1)).Schema(tt.schema)
			require.ErrorIs(t, err, fakefy.ErrInvalid)
			require.EqualError(t, err, "fakefy: invalid schema: "+tt.want)
		})
	}
}

func TestSchema_AnAllOfOfManyAnyOfsEnds(t *testing.T) {
	// Each anyOf of an allOf picks a branch of its own: every combination of them would be millions of schemas.
	members := make([]string, 0, 9)
	for i := range 9 {
		members = append(members, fmt.Sprintf(`{"anyOf": [{"minimum": %d}, {"maximum": 1000}, {"multipleOf": 1}, {"type": "integer"}, {"minimum": -5}, {"maximum": 500}]}`, i))
	}

	sch, doc := compile(t, `{"type": "integer", "allOf": [`+strings.Join(members, ", ")+`]}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	requireValid(t, sch, v)
}

func TestSchema_MakesTheItemsOfATuple(t *testing.T) {
	// OpenAPI 3.0 and draft 4 write a tuple as a list of items, and additionalItems for the rest.
	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{
		"type":            "array",
		"items":           []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "const": "x"}},
		"additionalItems": false,
		"minItems":        2,
	})
	require.NoError(t, err)

	items, ok := v.([]any)
	require.True(t, ok)
	require.Len(t, items, 2)
	require.IsType(t, int64(0), items[0])
	require.Equal(t, "x", items[1])
}

func TestSchema_NumbersOfTheSchemaComeBackAsDecodedJSON(t *testing.T) {
	// The doc promises int64 and float64: a caller that switches on them must not meet a json.Number or an int.
	_, doc := compile(t, `{"type": "object", "required": ["i", "n", "e"], "properties": {
		"i": {"type": "integer", "example": 3}, "n": {"const": 2.5}, "e": {"enum": [7]}}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"i": int64(3), "n": 2.5, "e": int64(7)}, v)

	v, err = fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"type": "object", "required": []any{"a", "b"}, "properties": map[string]any{
		"a": map[string]any{"const": uint32(3)}, "b": map[string]any{"type": "integer", "minimum": uint16(7), "maximum": int8(7)},
	}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": int64(3), "b": int64(7)}, v)
}

func TestSchema_FailsWithErrUnresolvedForARefOutsideTheDocument(t *testing.T) {
	for _, ref := range []string{"#/$defs/Missing", "other.json#/$defs/Pet", "#/properties/0"} {
		t.Run(ref, func(t *testing.T) {
			_, err := fakefy.New().Schema(map[string]any{"$ref": ref, "properties": make(map[string]any)})
			require.ErrorIs(t, err, fakefy.ErrUnresolved)
			require.ErrorContains(t, err, fmt.Sprintf("fakefy: unresolved $ref: at the root: %q: ", ref))
		})
	}
}

func TestSchema_TheDiscriminatorNamesTheBranchPicked(t *testing.T) {
	// OpenAPI clients read the discriminator to pick the type: a value whose tag names another branch breaks them.
	schema := map[string]any{
		"$defs": map[string]any{
			"Dog": map[string]any{"type": "object", "required": []any{"kind", "bark"}, "properties": map[string]any{
				"kind": map[string]any{"type": "string"}, "bark": map[string]any{"type": "boolean"},
			}},
			"Cat": map[string]any{"type": "object", "required": []any{"kind", "lives"}, "properties": map[string]any{
				"kind": map[string]any{"type": "string"}, "lives": map[string]any{"type": "integer", "maximum": 9},
			}},
		},
		"oneOf": []any{map[string]any{"$ref": "#/$defs/Dog"}, map[string]any{"$ref": "#/$defs/Cat"}},
		"discriminator": map[string]any{
			"propertyName": "kind",
			"mapping":      map[string]any{"dog": "#/$defs/Dog"},
		},
	}

	seen := make(map[any]bool)

	for seed := range uint64(_seeds) {
		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(schema)
		require.NoError(t, err)

		obj, ok := v.(map[string]any)
		require.True(t, ok)

		if _, dog := obj["bark"]; dog {
			require.Equal(t, "dog", obj["kind"], "the mapping names the Dog branch")
		} else {
			require.Equal(t, "Cat", obj["kind"], "without a mapping, the name of the $ref names the branch")
		}

		seen[obj["kind"]] = true
	}

	require.Len(t, seen, 2, "both branches are picked")
}

func TestSchema_TheDiscriminatorKeepsAValueTheBranchFixes(t *testing.T) {
	// A branch whose schema fixes its tag, as specs often do with a const, validates only with that value.
	_, doc := compile(t, `{
		"$defs": {"Dog": {"type": "object", "required": ["kind"], "properties": {"kind": {"const": "dog"}}}},
		"oneOf": [{"$ref": "#/$defs/Dog"}], "discriminator": {"propertyName": "kind"}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"kind": "dog"}, v)
}

func TestSchema_AValueChangedAfterLeavesTheSchemaAsItWas(t *testing.T) {
	// A caller fills a value in, such as a path parameter: the next value from the same example must not show it.
	example := map[string]any{"tags": []any{"a"}}
	schema := map[string]any{"type": "object", "example": example}

	v, err := fakefy.New().Schema(schema)
	require.NoError(t, err)

	obj, ok := v.(map[string]any)
	require.True(t, ok)

	tags, ok := obj["tags"].([]any)
	require.True(t, ok)

	tags[0] = "changed"
	obj["id"] = 1

	require.Equal(t, map[string]any{"tags": []any{"a"}}, example)
}

func TestSchema_LeavesTheSchemaAsItWas(t *testing.T) {
	// A generator makes many values from one spec: a value that writes into the spec's examples changes the next one.
	example := map[string]any{"bark": true}
	schema := map[string]any{
		"oneOf":         []any{map[string]any{"$ref": "#/$defs/Dog"}},
		"discriminator": map[string]any{"propertyName": "kind"},
		"$defs":         map[string]any{"Dog": map[string]any{"type": "object", "example": example}},
	}

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(schema)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"bark": true, "kind": "Dog"}, v)
	require.Equal(t, map[string]any{"bark": true}, example)
}

func TestSchema_RandomSchemasMakeValidValues(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		raw := schemaGen(3).Draw(t, "schema")
		seed := rapid.Uint64().Draw(t, "seed")

		b, err := json.Marshal(raw)
		require.NoError(t, err)

		sch, doc := compile(t, string(b))

		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
		require.NoError(t, err)
		requireValid(t, sch, v)
	})
}

// compile returns the validator of schema, a JSON document, with formats asserted, and the document as fakefy gets it
// from a JSON decoder that keeps numbers as json.Number.
func compile(t require.TestingT, schema string) (*jsonschema.Schema, map[string]any) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	require.NoError(t, c.AddResource("schema.json", doc))

	sch, err := c.Compile("schema.json")
	require.NoError(t, err)

	m, ok := doc.(map[string]any)
	require.True(t, ok)

	return sch, m
}

// requireValid fails unless v, written as JSON and read back, as a mock file holds it, validates against sch.
func requireValid(t require.TestingT, sch *jsonschema.Schema, v any) {
	b, err := json.Marshal(v)
	require.NoError(t, err)

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)
	require.NoError(t, sch.Validate(inst), "value: %s", b)
}

// Generators

// schemaGen draws JSON Schemas whose values fakefy must make: every keyword it reads, with bounds that some value
// meets, nested up to depth levels.
func schemaGen(depth int) *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		kinds := []string{"string", "pattern", "integer", "allOf", "number", "boolean", "null", "enum"}
		if depth > 0 {
			kinds = append(kinds, "object", "array")
		}

		switch rapid.SampledFrom(kinds).Draw(t, "kind") {
		case "string":
			s := map[string]any{"type": "string"}
			if f := rapid.SampledFrom([]string{"", "date-time", "date", "time", "email", "uuid", "ipv4", "ipv6", "uri", "hostname", "duration"}).Draw(t, "format"); f != "" {
				s["format"] = f
				return s
			}

			lo := rapid.IntRange(0, 5).Draw(t, "minLength")
			s["minLength"] = lo
			s["maxLength"] = lo + rapid.IntRange(0, 10).Draw(t, "extra")

			return s
		case "pattern":
			lo := rapid.IntRange(2, 6).Draw(t, "minLength")

			return map[string]any{
				"type":      "string",
				"pattern":   rapid.SampledFrom([]string{"^[a-z]+$", "^[0-9a-f]*$", "^[A-Z][a-z]+$", "^(ab|c)+-?[0-9]*$"}).Draw(t, "pattern"),
				"minLength": lo,
				"maxLength": lo + rapid.IntRange(0, 4).Draw(t, "extra"),
			}
		case "allOf":
			lo := rapid.IntRange(-1000, 1000).Draw(t, "minimum")

			return map[string]any{"allOf": []any{
				map[string]any{"type": "integer", "minimum": lo, "multipleOf": rapid.IntRange(1, 5).Draw(t, "a")},
				map[string]any{"type": "number", "maximum": lo + 100, "multipleOf": rapid.IntRange(1, 5).Draw(t, "b")},
			}}
		case "integer":
			lo := rapid.IntRange(-1000, 1000).Draw(t, "minimum")
			s := map[string]any{"type": "integer", "minimum": lo, "maximum": lo + rapid.IntRange(10, 100).Draw(t, "width")}
			if rapid.Bool().Draw(t, "multiple") {
				s["multipleOf"] = rapid.IntRange(1, 5).Draw(t, "multipleOf")
			}

			return s
		case "number":
			lo := rapid.Float64Range(-1e6, 1e6).Draw(t, "minimum")
			return map[string]any{"type": "number", "minimum": lo, "exclusiveMaximum": lo + rapid.Float64Range(0.5, 100).Draw(t, "width")}
		case "boolean":
			return map[string]any{"type": "boolean"}
		case "null":
			return map[string]any{"type": "null"}
		case "enum":
			return map[string]any{"enum": []any{rapid.String().Draw(t, "a"), rapid.Int().Draw(t, "b")}}
		case "object":
			props := make(map[string]any)
			var required []any

			for i := range rapid.IntRange(0, 3).Draw(t, "properties") {
				name := fmt.Sprintf("%s%d", rapid.SampledFrom([]string{"id", "email", "name", "createdAt", "price", "age", "url", "x"}).Draw(t, "name"), i)
				props[name] = schemaGen(depth-1).Draw(t, name)

				if rapid.Bool().Draw(t, "required") {
					required = append(required, name)
				}
			}

			s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
			if len(required) > 0 {
				s["required"] = required
			}

			return s
		default:
			lo := rapid.IntRange(0, 2).Draw(t, "minItems")
			return map[string]any{"type": "array", "items": schemaGen(depth-1).Draw(t, "items"), "minItems": lo, "maxItems": lo + rapid.IntRange(0, 3).Draw(t, "extra")}
		}
	})
}
