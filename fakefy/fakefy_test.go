package fakefy_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"uuid"

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
		{name: "a schema that refers to its root", schema: `{"type": "object", "properties": {"child": {"$ref": "#"}}}`},
		{name: "a ref to an item of a list", schema: `{"type": "array", "prefixItems": [{"type": "integer"}, {"$ref": "#/prefixItems/0"}], "minItems": 2, "maxItems": 2}`},
		{name: "optional items no value of the bounds satisfies", schema: `{"type": "array", "items": {"type": "integer", "minimum": 5, "maximum": 1}}`},
		{name: "allOf maximums", schema: `{"allOf": [{"type": "integer", "maximum": 10}, {"maximum": 5}]}`},
		{name: "allOf prefix items", schema: `{"type": "array", "minItems": 2, "maxItems": 2, "allOf": [{"prefixItems": [{"type": "integer"}]}, {"prefixItems": [{"minimum": 3, "maximum": 4}, {"type": "boolean"}]}]}`},
		{name: "allOf unique items", schema: `{"type": "array", "maxItems": 3, "allOf": [{"uniqueItems": true, "items": {"enum": [1, 2, 3]}}, {"uniqueItems": false}]}`},
		{name: "allOf items one of which is true", schema: `{"type": "array", "allOf": [{"items": {"type": "integer"}}, {"items": true}]}`},
		{name: "allOf type lists that share two types", schema: `{"allOf": [{"type": ["string", "null"]}, {"type": ["string", "null", "integer"]}]}`},
		{name: "allOf of a closed object and its own property", schema: `{"allOf": [
			{"type": "object", "required": ["a"], "properties": {"a": {"type": "string"}}, "additionalProperties": false},
			{"properties": {"a": {"maxLength": 3}}}]}`},
		{name: "unique items of objects", schema: `{"type": "array", "uniqueItems": true, "minItems": 2, "maxItems": 3, "items": {"enum": [{"a": 1}, {"a": 2}, {"b": 1}, {"a": 1, "b": 2}]}}`},
		{name: "unique items of arrays", schema: `{"type": "array", "uniqueItems": true, "minItems": 2, "maxItems": 3, "items": {"enum": [[1], [1, 2], [2], "x"]}}`},
		{name: "allOf enums of objects", schema: `{"allOf": [{"enum": [{"a": 1}, [1]]}, {"enum": [{"a": 1}]}]}`},
		{name: "a type list of null", schema: `{"type": ["null"]}`},
		{name: "items without a type", schema: `{"minItems": 1, "items": {"type": "integer"}}`},
		{name: "a minimum without a type", schema: `{"minimum": 3}`},
		{name: "a known name shorter than minLength", schema: `{"type": "object", "required": ["city"], "properties": {"city": {"type": "string", "minLength": 40}}}`},
		{name: "minProperties past a declared property1", schema: `{"type": "object", "minProperties": 2, "required": ["property1"], "properties": {"property1": {"type": "integer"}}}`},
		{name: "optional properties past maxProperties", schema: `{"type": "object", "maxProperties": 1, "properties": {"a": {"type": "integer"}, "b": {"type": "integer"}, "c": {"type": "integer"}}}`},
		{name: "exclusive bounds a few doubles apart", schema: `{"type": "number", "exclusiveMinimum": 1, "exclusiveMaximum": 1.0000000000000009}`},
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

func TestSchema_SchemasAValueSatisfiesMakeOne(t *testing.T) {
	// Each schema has a value: the generator must find one, not fail, nor panic, nor return one that doesn't validate.
	leaf := `{"type": "object", "minProperties": 1, "additionalProperties": false, "properties": {"leaf": {"type": "string"}}}`
	for range 4 {
		leaf = `{"type": "object", "required": ["p"], "properties": {"p": ` + leaf + `}}`
	}

	// A tree each node of which has five children, each a nullable node, as OpenAPI 3.1 writes one.
	children := make([]string, 5)
	for i := range children {
		children[i] = fmt.Sprintf(`"c%d": {"anyOf": [{"$ref": "#/$defs/N"}, {"type": "null"}]}`, i)
	}

	tree := `{"$defs": {"N": {"type": "object", "required": ["c0", "c1", "c2", "c3", "c4"], "properties": {` + strings.Join(children, ", ") + `}}},
		"$ref": "#/$defs/N"}`

	anyOfs := strings.Repeat(`{"anyOf": [{"minimum": 0}, {"maximum": 50}]}, `, 33)

	tests := []struct {
		name   string
		schema string
	}{
		{name: "a tree of nullable nodes", schema: tree},
		{name: "an allOf of more anyOfs than the levels a value nests", schema: `{"type": "integer", "allOf": [` + anyOfs + `{}]}`},
		{name: "an items schema of allOf and the prefixItems of another", schema: `{"type": "array", "allOf": [
			{"items": {"type": "integer", "maximum": 0}}, {"prefixItems": [{"type": "integer"}]}]}`},
		{name: "allOf of two prefixItems and an items schema", schema: `{"type": "array", "minItems": 2, "allOf": [
			{"prefixItems": [{"type": "integer"}], "items": {"type": "integer", "maximum": 0}},
			{"prefixItems": [{"type": "integer"}, {"type": "integer"}]}]}`},
		{name: "minProperties of patternProperties alone", schema: `{"type": "object", "minProperties": 1,
			"patternProperties": {".*": {"type": "integer"}}, "additionalProperties": false}`},
		{name: "minProperties of a key patternProperties match", schema: `{"type": "object", "minProperties": 1,
			"patternProperties": {"^property": {"type": "integer"}}, "additionalProperties": {"type": "string"}}`},
		{name: "minProperties of patterns that each name one key", schema: `{"type": "object", "minProperties": 2,
			"patternProperties": {"^a$": {"type": "string"}, "^b$": {"type": "string"}}, "additionalProperties": false}`},
		{name: "minProperties of every key a pattern names", schema: `{"type": "object", "minProperties": 10,
			"patternProperties": {"^[0-9]$": {"type": "integer"}}, "additionalProperties": false}`},
		{name: "minProperties of a pattern that names few keys", schema: `{"type": "object", "minProperties": 2,
			"patternProperties": {"^x-[a-z]+$": {"type": "integer"}}, "additionalProperties": false}`},
		{name: "allOf additionalProperties and the patternProperties of another", schema: `{"allOf": [
			{"type": "object", "additionalProperties": {"type": "integer"}},
			{"required": ["x1"], "patternProperties": {"^x": {"minimum": 0}}}]}`},
		{name: "allOf additionalProperties of named properties and the patternProperties of another", schema: `{"allOf": [
			{"type": "object", "properties": {"x_id": {"type": "string"}}, "additionalProperties": false},
			{"required": ["x_id"], "patternProperties": {"^x": {"minLength": 2}}}]}`},
		{name: "allOf patternProperties and the additionalProperties of another", schema: `{"allOf": [
			{"required": ["x1"], "patternProperties": {"^x": {"minimum": 0}}},
			{"type": "object", "additionalProperties": {"type": "integer"}}]}`},
		{name: "allOf of draft 4 bounds, one exclusive", schema: `{"$schema": "http://json-schema.org/draft-04/schema#",
			"type": "integer", "maximum": 1, "allOf": [{"minimum": 0, "exclusiveMinimum": true}, {"minimum": 1}]}`},
		{name: "allOf of draft 4 upper bounds, one exclusive", schema: `{"$schema": "http://json-schema.org/draft-04/schema#",
			"type": "integer", "minimum": 1, "allOf": [{"maximum": 2, "exclusiveMaximum": true}, {"maximum": 1}]}`},
		{name: "a number whose only float64 is the inclusive maximum", schema: `{"type": "number", "exclusiveMinimum": 1, "maximum": 1.0000000000000002}`},
		{name: "a number whose only float64 is the least above 0", schema: `{"type": "number", "exclusiveMinimum": 0, "maximum": 5e-324}`},
		{name: "a const past int64", schema: `{"const": 12345678901234567890}`},
		{name: "unique items of an enum past int64", schema: `{"type": "array", "uniqueItems": true, "minItems": 2,
			"items": {"enum": [18446744073709551615, 18446744073709551614]}}`},
		{name: "an email of at most 10 characters", schema: `{"type": "string", "format": "email", "maxLength": 10}`},
		{name: "a hostname of at most 6 characters", schema: `{"type": "string", "format": "hostname", "maxLength": 6}`},
		{name: "a uri of at most 14 characters", schema: `{"type": "string", "format": "uri", "maxLength": 14}`},
		{name: "unique integers past the range a value has without bounds", schema: `{"type": "array", "uniqueItems": true, "minItems": 150,
			"items": {"type": "integer"}}`},
		{name: "unique multiples past the range a value has without bounds", schema: `{"type": "array", "uniqueItems": true, "minItems": 150,
			"items": {"type": "integer", "multipleOf": 5, "minimum": 0}}`},
		{name: "unique months past the range the name gives", schema: `{"type": "object", "required": ["month"], "properties": {
			"month": {"type": "array", "uniqueItems": true, "minItems": 20, "items": {"type": "integer"}}}}`},
		{name: "a multiple whose next one past the bounds overflows", schema: `{"type": "number", "multipleOf": 1e308}`},
		{name: "unique items an example repeats", schema: `{"type": "array", "minItems": 2, "uniqueItems": true, "items": {"type": "string", "example": "x"}}`},
		{name: "a required property allOf allows by pattern", schema: `{"allOf": [
			{"type": "object", "patternProperties": {"^x": {"type": "integer"}}, "additionalProperties": false},
			{"required": ["x1"], "properties": {"x1": {"type": "integer"}}}]}`},
		{name: "a decimal multiple that is the only value", schema: `{"type": "number", "minimum": 0.07, "maximum": 0.07, "multipleOf": 0.01}`},
		{name: "an integer bound past what a float64 holds exactly", schema: `{"type": "integer", "minimum": 9007199254740993, "maximum": 9007199254740993}`},
		{name: "a nullable type whose other type has no value", schema: `{"type": ["integer", "null"], "minimum": 2, "maximum": 1}`},
		{name: "a nullable object that requires itself", schema: `{"type": ["object", "null"], "required": ["next"], "properties": {"next": {"$ref": "#"}}}`},
		{name: "minProperties past the depth that leaves out optional properties", schema: leaf},
		{name: "an unanchored pattern shorter than minLength", schema: `{"type": "string", "pattern": "[0-9]", "minLength": 10}`},
		{name: "a format and a pattern", schema: `{"type": "string", "format": "uuid", "pattern": "^[a-f0-9-]+$"}`},
		{name: "a format whose values start with what a pattern writes", schema: `{"type": "string", "format": "email", "pattern": "^x"}`},
		{name: "a format whose values end with what a pattern writes", schema: `{"type": "string", "format": "hostname", "pattern": "\\.io$"}`},
		{name: "a format whose values the pattern doesn't match", schema: `{"type": "string", "format": "email", "pattern": "^a@b\\.co$"}`},
		{name: "an integer range past both ends of an int64", schema: `{"type": "integer", "minimum": -1e19, "maximum": 1e19}`},
		{name: "allOf of two tuples", schema: `{"$schema": "http://json-schema.org/draft-04/schema#", "allOf": [
			{"type": "array", "items": [{"type": "integer"}], "additionalItems": false},
			{"items": [{"type": "integer"}, {"type": "string"}]}]}`},
		{name: "allOf of a tuple and an items schema", schema: `{"$schema": "http://json-schema.org/draft-04/schema#", "allOf": [
			{"type": "array", "minItems": 2, "items": [{"type": "integer"}], "additionalItems": {"type": "string"}},
			{"items": {"enum": [3, "a"]}}]}`},
		{name: "a property two patternProperties match", schema: `{"type": "object", "required": ["ab"], "patternProperties": {"^a": {"type": "string"}, "^ab$": {"const": "z"}}}`},
		{name: "a discriminated branch whose const fixes the tag", schema: `{
			"$defs": {"Cat": {"const": {"petType": "feline", "name": "x"}}},
			"oneOf": [{"$ref": "#/$defs/Cat"}],
			"discriminator": {"propertyName": "petType", "mapping": {"cat": "#/$defs/Cat"}}}`},
		{name: "unique integers past what a float64 holds exactly", schema: `{"type": "array", "uniqueItems": true, "minItems": 2,
			"items": {"type": "integer", "minimum": 9007199254740992, "maximum": 9007199254740993}}`},
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
		{name: "a format no string of the pattern has", schema: `{"type": "string", "format": "uuid", "pattern": "^z"}`, want: `at the root: pattern "^z": no string of format "uuid" after 20 tries`},
		{name: "a format shorter than minLength", schema: `{"type": "string", "format": "uuid", "minLength": 40}`, want: `at the root: format "uuid" makes no string of at least 40 characters`},
		{name: "minProperties above maxProperties", schema: `{"type": "object", "minProperties": 3, "maxProperties": 1}`, want: "at the root: minProperties 3 is above maxProperties 1"},
		{name: "allOf of types that share no value", schema: `{"allOf": [{"type": "string"}, {"type": "integer"}]}`, want: `at the root: allOf: no type is both of ["string"] and ["integer"]`},
		{name: "allOf of enums that share no value", schema: `{"allOf": [{"enum": ["a"]}, {"enum": ["b"]}]}`, want: "at the root: allOf: no value is in both enums"},
		{name: "allOf of different consts", schema: `{"allOf": [{"const": 1}, {"const": 2}]}`, want: "at the root: allOf: two consts"},
		{name: "minProperties past what additionalProperties allows", schema: `{"type": "object", "minProperties": 2, "properties": {"a": {"type": "integer"}}, "additionalProperties": false}`, want: "at the root: expected 2 properties, not 1"},
		{name: "minProperties of additionalProperties no value satisfies", schema: `{"type": "object", "minProperties": 1, "additionalProperties": {"type": "integer", "minimum": 3, "maximum": 1}}`, want: `at "/property1": minimum 3 is above maximum 1`},
		{name: "required properties past maxProperties", schema: `{"type": "object", "required": ["a", "b"], "maxProperties": 1}`, want: "at the root: 2 required properties exceed maxProperties 1"},
		{name: "a required property additionalProperties refuses", schema: `{"type": "object", "required": ["x"], "additionalProperties": {"type": "integer", "minimum": 3, "maximum": 1}}`, want: `at "/x": minimum 3 is above maximum 1`},
		{name: "an item before minItems no value satisfies", schema: `{"type": "array", "minItems": 1, "items": {"type": "integer", "minimum": 5, "maximum": 1}}`, want: `at "/0": minimum 5 is above maximum 1`},
		{name: "a multiple past int64", schema: `{"type": "integer", "multipleOf": 1e300}`, want: "at the root: no integer is a multiple of 1e+300"},
		{name: "no number between bounds that meet", schema: `{"type": "number", "minimum": 1, "exclusiveMaximum": 1}`, want: "at the root: no number between 1 and 1, exclusive"},
		{name: "no decimal multiple within the bounds", schema: `{"type": "number", "multipleOf": 0.7, "minimum": 0.1, "maximum": 0.6}`, want: "at the root: no multiple of 0.7 between 0.1 and 0.6"},
		{name: "number bounds that cross", schema: `{"type": "number", "minimum": 5, "maximum": 1}`, want: "at the root: minimum 5 is above maximum 1"},
		{name: "required properties without end, through anyOf", schema: `{"$defs": {"N": {"type": "object", "required": ["n"], "properties": {
			"n": {"anyOf": [{"$ref": "#/$defs/N"}, {"$ref": "#/$defs/N"}, {"$ref": "#/$defs/N"}]}}}}, "$ref": "#/$defs/N"}`, want: `at "/n": no value after 10000 branches failed`},
		{name: "minProperties past the keys a pattern names", schema: `{"type": "object", "minProperties": 2,
			"patternProperties": {"^a$": {}}, "additionalProperties": false}`, want: "at the root: expected 2 properties, not 1"},
		{name: "minProperties of a pattern no key matches", schema: `{"type": "object", "minProperties": 1,
			"patternProperties": {"[^\\x00-\\x{10FFFF}]": {}}, "additionalProperties": false}`, want: "at the root: expected 1 properties, not 0"},
		{name: "no float64 between the bounds", schema: `{"type": "number", "exclusiveMinimum": 1, "exclusiveMaximum": 1.0000000000000002}`, want: "at the root: no float64 between 1 and 1.0000000000000002"},
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
	deep := map[string]any{"type": "object", "minProperties": 1, "properties": map[string]any{"a": 3}}
	for range 4 {
		deep = map[string]any{"type": "object", "required": []any{"p"}, "properties": map[string]any{"p": deep}}
	}

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
		{name: "patternProperties that don't compile, in an allOf with additionalProperties", schema: map[string]any{"allOf": []any{
			map[string]any{"type": "object", "additionalProperties": true},
			map[string]any{"required": []any{"a"}, "patternProperties": map[string]any{"(": true}},
		}}, want: "at the root: patternProperties \"(\": compiling: error parsing regexp: missing closing ): `(`"},
		{name: "patternProperties that don't compile, for minProperties", schema: map[string]any{"type": "object", "minProperties": 1, "patternProperties": map[string]any{"(": true}}, want: "at the root: patternProperties \"(\": compiling: error parsing regexp: missing closing ): `(`"},
		{name: "a number in an allOf", schema: map[string]any{"allOf": []any{3}}, want: "at the root: expected a schema object or boolean, not int"},
		{name: "a $ref to a number", schema: map[string]any{"$defs": map[string]any{"a": 3}, "$ref": "#/$defs/a"}, want: "at the root: expected a schema object or boolean, not int"},
		{name: "patternProperties that don't compile", schema: map[string]any{"type": "object", "properties": map[string]any{"a": true}, "patternProperties": map[string]any{"(": true}}, want: "at the root: patternProperties \"(\": compiling: error parsing regexp: missing closing ): `(`"},
		{name: "patternProperties of a required property that don't compile", schema: map[string]any{"type": "object", "required": []any{"a"}, "patternProperties": map[string]any{"(": true}}, want: "at the root: patternProperties \"(\": compiling: error parsing regexp: missing closing ): `(`"},
		{name: "a maxItems that isn't whole", schema: map[string]any{"type": "array", "maxItems": 1.5}, want: "at the root: maxItems must be a whole number from 0, not 1.5"},
		{name: "a negative maxLength", schema: map[string]any{"type": "string", "maxLength": -1}, want: "at the root: maxLength must be a whole number from 0, not -1"},
		{name: "a negative minProperties", schema: map[string]any{"type": "object", "minProperties": -1}, want: "at the root: minProperties must be a whole number from 0, not -1"},
		{name: "null as a schema", schema: map[string]any{"properties": map[string]any{"a": nil}}, want: `at "/a": expected a schema object or boolean, not <nil>`},
		{name: "an optional property minProperties needs past the depth that leaves it out", schema: deep, want: `at "/p/p/p/p/a": expected a schema object or boolean, not int`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fakefy.New(fakefy.WithSeed(1)).Schema(tt.schema)
			require.ErrorIs(t, err, fakefy.ErrInvalid)
			require.EqualError(t, err, "fakefy: invalid schema: "+tt.want)
		})
	}
}

func TestSchema_FailsWhenNoValueValidates(t *testing.T) {
	// A keyword the generator doesn't read, such as not, still holds: a value that breaks it is never returned.
	_, doc := compile(t, `{"type": "string", "not": {"type": "string"}}`)

	_, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
	require.ErrorContains(t, err, "fakefy: no value satisfies the schema: at the root: no value of 20 validates: ")
}

func TestSchema_FailsOnAOneOfOfNoSchemas(t *testing.T) {
	_, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"oneOf": []any{}})
	require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
	require.EqualError(t, err, "fakefy: no value satisfies the schema: at the root: oneOf has no schemas")
}

func TestSchema_ARefChainEnds(t *testing.T) {
	// $refs that only name each other never reach a schema: the generator stops rather than follow them forever.
	defs := make(map[string]any)
	for i := range 40 {
		defs[fmt.Sprintf("a%d", i)] = map[string]any{"$ref": fmt.Sprintf("#/$defs/a%d", i+1)}
	}

	_, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"$defs": defs, "$ref": "#/$defs/a0"})
	require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
	require.EqualError(t, err, "fakefy: no value satisfies the schema: at the root: $refs chain past 32 schemas")
}

func TestWithDefs_ARefChainEndsWhereItWouldWithTheSchemasUnderTheDefs(t *testing.T) {
	// A chain of $refs to shared schemas ends at the same length as one to the schema's own: a chain the embedded
	// schemas allow, such as 32 $refs, makes a value with Defs too.
	tests := []struct {
		refs int
		want any
		err  error
	}{
		{refs: 31, want: "ok"},
		{refs: 32, want: "ok"},
		{refs: 33, err: fakefy.ErrUnsatisfiable},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.refs), func(t *testing.T) {
			defs := make(map[string]any, tt.refs)
			for i := range tt.refs {
				def := map[string]any{"const": "ok"}
				if i+1 < tt.refs {
					def = map[string]any{"$ref": fmt.Sprintf("#/$defs/N%d", i+1)}
				}

				defs[fmt.Sprintf("N%d", i)] = def
			}

			for _, f := range []func() (any, error){
				func() (any, error) {
					return fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"$ref": "#/$defs/N0", "$defs": defs})
				},
				func() (any, error) {
					return fakefy.New(fakefy.WithSeed(1), fakefy.WithDefs(fakefy.NewDefs(defs))).Schema(map[string]any{"$ref": "#/$defs/N0"})
				},
			} {
				v, err := f()
				require.ErrorIs(t, err, tt.err)
				require.Equal(t, tt.want, v)
			}
		})
	}
}

func TestSchema_ASchemaThatIsNotJSONStillMakesValues(t *testing.T) {
	// A Go caller's schema can hold what JSON can't, such as an infinite number in an extension keyword: the
	// validator can't read it, so the values go unchecked rather than fail.
	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"type": "integer", "minimum": 3, "maximum": 3, "x-limit": math.Inf(1)})
	require.NoError(t, err)
	require.Equal(t, int64(3), v)
}

func TestSchema_AnAllOfBoundThatIsNotANumberIsIgnored(t *testing.T) {
	for _, all := range [][]any{
		{map[string]any{"type": "integer", "minimum": "5", "maximum": 7}, map[string]any{"minimum": 7}},
		{map[string]any{"type": "integer", "minimum": 7, "maximum": 7}, map[string]any{"minimum": "5"}},
	} {
		v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"allOf": all})
		require.NoError(t, err)
		require.Equal(t, int64(7), v)
	}

	for _, all := range [][]any{
		{map[string]any{"type": "string", "minLength": "5", "maxLength": 3}, map[string]any{"minLength": 3}},
		{map[string]any{"type": "string", "minLength": 3, "maxLength": 3}, map[string]any{"minLength": "5"}},
		{map[string]any{"type": "string", "minLength": 2, "maxLength": 3}, map[string]any{"minLength": 3}},
	} {
		v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"allOf": all})
		require.NoError(t, err)
		require.Len(t, v, 3)
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

func TestSchema_AnAllOfOfASchemaTwiceManyLevelsDownEnds(t *testing.T) {
	// Specs compose schemas from shared parts, so one part can be reached many ways: each must be merged once, or a
	// level more doubles the work. A value a validator checks takes it as many ways: an infinite number the validator
	// can't read leaves the work to the generator alone.
	defs := map[string]any{"A30": map[string]any{"type": "string", "pattern": "^a", "maxLength": 3}}
	for i := range 30 {
		next := map[string]any{"$ref": fmt.Sprintf("#/$defs/A%d", i+1)}
		defs[fmt.Sprintf("A%d", i)] = map[string]any{"allOf": []any{next, maps.Clone(next)}}
	}

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"$defs": defs, "$ref": "#/$defs/A0", "x-limit": math.Inf(1)})
	require.NoError(t, err)
	require.Regexp(t, `^a.{0,2}$`, v)
}

func TestSchema_AValueOfManySchemasIsMade(t *testing.T) {
	// A large response, such as a page of items each of many fields, is made of many schemas: only branches that fail
	// count toward giving up.
	sch, doc := compile(t, `{"type": "array", "minItems": 400, "items": {"type": "array", "minItems": 400, "items": {"type": "boolean"}}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	requireValid(t, sch, v)
}

func TestSchema_AnAllOfOfEnumsOfGoListsMergesThem(t *testing.T) {
	// A Go caller's enum may hold lists of any Go type: comparing two of one type that == can't compare must not panic.
	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"allOf": []any{
		map[string]any{"enum": []any{[]string{"a"}, []string{"b"}}},
		map[string]any{"enum": []any{[]string{"b"}}},
	}})
	require.NoError(t, err)
	require.Equal(t, []any{"b"}, v)
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

func TestSchema_GoNumbersOfTheSchemaComeBackAsDecodedJSON(t *testing.T) {
	// A Go caller writes schemas with any number type: values come back as int64 or float64 all the same, and bounds
	// of any type hold.
	tests := []struct {
		name string
		n    any
		want any
	}{
		{name: "int", n: 3, want: int64(3)},
		{name: "int8", n: int8(3), want: int64(3)},
		{name: "int16", n: int16(3), want: int64(3)},
		{name: "int32", n: int32(3), want: int64(3)},
		{name: "int64", n: int64(3), want: int64(3)},
		{name: "uint", n: uint(3), want: int64(3)},
		{name: "uint8", n: uint8(3), want: int64(3)},
		{name: "uint16", n: uint16(3), want: int64(3)},
		{name: "uint32", n: uint32(3), want: int64(3)},
		{name: "uint64", n: uint64(3), want: int64(3)},
		{name: "a uint64 past int64", n: uint64(math.MaxUint64), want: uint64(math.MaxUint64)},
		{name: "float32", n: float32(2.5), want: 2.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"type": "number", "example": tt.n})
			require.NoError(t, err)
			require.Equal(t, tt.want, v)
		})
	}

	for _, n := range []any{3, int8(3), int16(3), int32(3), uint(3), uint8(3), uint16(3), uint32(3), uint64(3), float32(3)} {
		t.Run(fmt.Sprintf("bounds of %T", n), func(t *testing.T) {
			v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"type": "number", "minimum": n, "maximum": n})
			require.NoError(t, err)
			require.Equal(t, float64(3), v)
		})
	}
}

func TestSchema_FailsWithErrUnresolvedForARefOutsideTheDocument(t *testing.T) {
	for _, ref := range []string{"#/$defs/Missing", "other.json#/$defs/Pet", "#/properties/0", "#/%zz", "#anchor", "#/allOf/9", "#/allOf/x", "#/type/x"} {
		t.Run(ref, func(t *testing.T) {
			_, err := fakefy.New().Schema(map[string]any{"$ref": ref, "properties": make(map[string]any), "allOf": []any{true}, "type": "object"})
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

func TestSchema_TheDiscriminatorNamesTheBranchAmongTheTagsTheBaseLists(t *testing.T) {
	// OpenAPI inheritance lists every tag in the base schema's enum: each branch still has its own.
	_, doc := compile(t, `{"$defs": {
		"Pet": {"type": "object", "required": ["petType"], "properties": {"petType": {"type": "string", "enum": ["Cat", "Dog"]}}},
		"Cat": {"allOf": [{"$ref": "#/$defs/Pet"}, {"type": "object", "required": ["lives"], "properties": {"lives": {"type": "integer"}}}]},
		"Dog": {"allOf": [{"$ref": "#/$defs/Pet"}, {"type": "object", "required": ["bark"], "properties": {"bark": {"type": "boolean"}}}]}},
		"oneOf": [{"$ref": "#/$defs/Cat"}, {"$ref": "#/$defs/Dog"}], "discriminator": {"propertyName": "petType"}}`)

	for seed := range uint64(_seeds) {
		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
		require.NoError(t, err)

		obj, ok := v.(map[string]any)
		require.True(t, ok)

		want := "Cat"
		if _, dog := obj["bark"]; dog {
			want = "Dog"
		}

		require.Equal(t, want, obj["petType"])
	}
}

func TestSchema_TheDiscriminatorMappingNamesABranchBySchemaName(t *testing.T) {
	// OpenAPI lets a mapping name a schema of components/schemas by its name, not only by a $ref.
	_, doc := compile(t, `{"$defs": {"Dog": {"type": "object", "properties": {"bark": {"type": "boolean"}}}},
		"oneOf": [{"$ref": "#/$defs/Dog"}], "discriminator": {"propertyName": "kind", "mapping": {"dog": "Dog"}}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)

	obj, ok := v.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "dog", obj["kind"])
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

func TestSchema_TheDiscriminatorTagsABranchThatDoesNotDeclareIt(t *testing.T) {
	_, doc := compile(t, `{
		"$defs": {"Dog": {"type": "object", "required": ["bark"], "properties": {"bark": {"const": true}}}},
		"oneOf": [{"$ref": "#/$defs/Dog"}], "discriminator": {"propertyName": "kind"}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"bark": true, "kind": "Dog"}, v)
}

func TestSchema_TheDiscriminatorLeavesAnInlineBranchAsMade(t *testing.T) {
	// Only a $ref, or the mapping, names a branch: an inline one has no name to tag the value with.
	_, doc := compile(t, `{
		"oneOf": [{"type": "object", "required": ["kind"], "properties": {"kind": {"type": "string", "example": "x"}}}],
		"discriminator": {"propertyName": "kind"}}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"kind": "x"}, v)
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

func TestSchema_GoListsAndObjectsOfTheSchemaComeBackAsDecodedJSON(t *testing.T) {
	// A Go caller writes examples of any list or object type: they come back as JSON decodes them, a []byte as the
	// copy of it JSON writes as base64.
	tests := []struct {
		name    string
		example any
		want    any
	}{
		{name: "a list", example: []string{"a"}, want: []any{"a"}},
		{name: "an array", example: [2]int{1, 2}, want: []any{int64(1), int64(2)}},
		{name: "an object", example: map[string]string{"k": "v"}, want: map[string]any{"k": "v"}},
		{name: "a nil list", example: []string(nil), want: nil},
		{name: "bytes", example: []byte("ab"), want: []byte("ab")},
		{name: "a list of a type of its own, which writes itself as JSON", example: jsontext.Value(`{"a":1}`), want: jsontext.Value(`{"a":1}`)},
		{name: "an object of a type of its own", example: url.Values{"a": {"1"}}, want: url.Values{"a": {"1"}}},
		{name: "an array of a type of its own", example: uuid.UUID{1}, want: uuid.UUID{1}},
		{name: "an object of keys that aren't strings", example: map[int]string{1: "a"}, want: map[int]string{1: "a"}},
		{name: "a struct", example: struct{ A int }{A: 1}, want: struct{ A int }{A: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"example": tt.example})
			require.NoError(t, err)
			require.Equal(t, tt.want, v)
		})
	}
}

func TestSchema_AValueOfAGoListExampleSharesNothingWithIt(t *testing.T) {
	// A Go caller writes examples of any list type: a value that is the example itself changes it when filled in.
	example := []string{"a"}

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(map[string]any{"type": "array", "example": example})
	require.NoError(t, err)
	require.Equal(t, []any{"a"}, v)

	v.([]any)[0] = "changed"

	require.Equal(t, []string{"a"}, example)
}

func TestSchema_AnOpenAPI30SchemaChecksItsValues(t *testing.T) {
	// OpenAPI 3.0 writes exclusiveMinimum as a boolean, which JSON Schema 2020-12 refuses: its values are checked as
	// draft 4 has them, so a oneOf branch that overlaps another never makes a value both match.
	draft4, _ := compile(t, `{"$schema": "http://json-schema.org/draft-04/schema#", "type": "integer", "minimum": 0,
		"exclusiveMinimum": true, "oneOf": [{"maximum": 50}, {"maximum": 100}]}`)
	schema := map[string]any{
		"type": "integer", "minimum": 0, "exclusiveMinimum": true,
		"oneOf": []any{map[string]any{"maximum": 50}, map[string]any{"maximum": 100}},
	}

	for seed := range uint64(_seeds) {
		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(schema)
		require.NoError(t, err)
		requireValid(t, draft4, v)
	}
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
	// Every schema a value satisfies, of the keywords fakefy reads, gets one: a generator that fails or writes an invalid
	// value breaks the mocks of a spec it was given.
	rapid.Check(t, func(t *rapid.T) {
		raw := rootGen(schemaGen(3)).Draw(t, "schema")
		seed := rapid.Uint64().Draw(t, "seed")

		b, err := json.Marshal(raw)
		require.NoError(t, err)

		sch, doc := compile(t, string(b))

		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
		require.NoError(t, err)
		requireValid(t, sch, v)
	})
}

func TestSchema_AnySchemaMakesAValidValueOrFailsWithASentinel(t *testing.T) {
	// A spec may hold a schema no value satisfies, or one that isn't valid: the generator must say so with an error a
	// caller can tell apart, never panic, and never return a value the schema refuses.
	rapid.Check(t, func(t *rapid.T) {
		raw := anySchemaGen(3).Draw(t, "schema")
		seed := rapid.Uint64().Draw(t, "seed")

		b, err := json.Marshal(raw)
		require.NoError(t, err)

		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)

		m, ok := doc.(map[string]any)
		require.True(t, ok)

		v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(m)
		if err != nil {
			require.Contains(t, []error{fakefy.ErrUnsatisfiable, fakefy.ErrInvalid, fakefy.ErrUnresolved}, sentinel(err))

			return
		}

		// A schema the validator can't compile, such as a draft-4 form, can't check the value.
		if sch, ok := tryCompile(b); ok {
			requireValid(t, sch, v)
		}
	})
}

func TestWithSeed_ARandomSchemaMakesTheSameValueForASeed(t *testing.T) {
	// Mock generators write fake values to files: a seed that made other bytes on the next run would show every file
	// as changed.
	rapid.Check(t, func(t *rapid.T) {
		raw := anySchemaGen(3).Draw(t, "schema")
		seed := rapid.Uint64().Draw(t, "seed")

		first, firstErr := fakefy.New(fakefy.WithSeed(seed)).Field("value", raw)
		second, secondErr := fakefy.New(fakefy.WithSeed(seed)).Field("value", raw)

		require.Equal(t, first, second)
		// The message quotes the validator, which lists the properties that fail in map order.
		require.Equal(t, sentinel(firstErr), sentinel(secondErr))
	})
}

func TestSchema_ARandomValueSharesNothingWithItsSchema(t *testing.T) {
	// A caller fills the value in, such as with a path parameter: a schema that changed with it would make the next
	// value from the change.
	rapid.Check(t, func(t *rapid.T) {
		raw := rootGen(schemaGen(3)).Draw(t, "schema")
		before, err := json.Marshal(raw)
		require.NoError(t, err)

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(raw)
		require.NoError(t, err)

		scribble(v)

		after, err := json.Marshal(raw)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after))
	})
}

func TestSchema_RandomIntegerBoundsMakeAMultipleWithinThemOrFail(t *testing.T) {
	// Integer bounds in specs reach past what a float64 holds exactly, such as int64 ids: the value must be the
	// integer the bounds allow, exactly, and a range with none must fail rather than round into one.
	rapid.Check(t, func(t *rapid.T) {
		lo := bigIntGen().Draw(t, "lo")
		hi := new(big.Int).Add(lo, big.NewInt(rapid.Int64Range(-10, 1_000_000).Draw(t, "width")))
		exLo, exHi := rapid.Bool().Draw(t, "exclusiveMinimum"), rapid.Bool().Draw(t, "exclusiveMaximum")
		step := rapid.SampledFrom([]string{"", "1", "3", "2.5", "1000", "0.5", "7"}).Draw(t, "multipleOf")

		schema := `{"type": "integer", "` + boundKey("minimum", exLo) + `": ` + lo.String() + `, "` + boundKey("maximum", exHi) + `": ` + hi.String()
		if step != "" {
			schema += `, "multipleOf": ` + step
		}

		sch, doc := compile(t, schema+"}")

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(doc)
		if !integerExists(lo, hi, exLo, exHi, step) {
			require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
			return
		}

		require.NoError(t, err)
		require.IsType(t, int64(0), v)
		requireValid(t, sch, v)
	})
}

func TestSchema_RandomDecimalBoundsMakeAMultipleWithinThemOrFail(t *testing.T) {
	// Prices and rates are decimals: a value a float64 rounds past a bound, or off a multiple, breaks the contract.
	rapid.Check(t, func(t *rapid.T) {
		lo := decimalGen().Draw(t, "lo")
		hi := new(big.Rat).Add(lo, decimalGen().Draw(t, "width"))
		hi.Abs(hi)
		if hi.Cmp(lo) < 0 {
			lo, hi = hi, lo
		}

		exLo, exHi := rapid.Bool().Draw(t, "exclusiveMinimum"), rapid.Bool().Draw(t, "exclusiveMaximum")
		step := rapid.SampledFrom([]string{"", "0.01", "0.05", "0.25", "1.5", "0.001", "3"}).Draw(t, "multipleOf")

		schema := `{"type": "number", "` + boundKey("minimum", exLo) + `": ` + lo.FloatString(4) + `, "` +
			boundKey("maximum", exHi) + `": ` + hi.FloatString(4)
		if step != "" {
			schema += `, "multipleOf": ` + step
		}

		sch, doc := compile(t, schema+"}")

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(doc)
		if !decimalExists(lo, hi, exLo, exHi, step) {
			require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
			return
		}

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

// compileMap compiles schema, a map, as [compile] does a JSON document.
func compileMap(t require.TestingT, schema map[string]any) (*jsonschema.Schema, map[string]any) {
	b, err := json.Marshal(schema)
	require.NoError(t, err)

	return compile(t, string(b))
}

// requireValid fails unless v, written as JSON and read back, as a mock file holds it, validates against sch.
func requireValid(t require.TestingT, sch *jsonschema.Schema, v any) {
	b, err := json.Marshal(v)
	require.NoError(t, err)

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)
	require.NoError(t, sch.Validate(inst), "value: %s", b)
}

// tryCompile returns the validator of schema, a JSON document, with formats asserted, when it compiles as JSON Schema
// 2020-12.
func tryCompile(schema []byte) (*jsonschema.Schema, bool) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, false
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, false
	}

	sch, err := c.Compile("schema.json")

	return sch, err == nil
}

// sentinel returns the error of fakefy that err wraps, or err when it wraps none.
func sentinel(err error) error {
	for _, s := range []error{fakefy.ErrUnsatisfiable, fakefy.ErrInvalid, fakefy.ErrUnresolved} {
		if errors.Is(err, s) {
			return s
		}
	}

	return err
}

// scribble changes every object and list of v, at every level.
func scribble(v any) {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			scribble(x)
			v[k] = "scribbled"
		}

		v["scribbled"] = true
	case []any:
		for i, x := range v {
			scribble(x)
			v[i] = "scribbled"
		}
	}
}

// boundKey returns key, or its exclusive form when exclusive.
func boundKey(key string, exclusive bool) string {
	if !exclusive {
		return key
	}

	return "exclusive" + strings.ToUpper(key[:1]) + key[1:]
}

// integerExists reports whether an int64 that is a multiple of step, an integer one when step is "", lies between lo
// and hi, each end excluded when its flag says so.
func integerExists(lo, hi *big.Int, exLo, exHi bool, step string) bool {
	r := big.NewRat(1, 1)
	if step != "" {
		m, _ := new(big.Rat).SetString(step)

		// The least multiple of m that is an integer: m's numerator.
		r.SetInt(m.Num())
	}

	k := r.Num()
	first := ceilDiv(maxInt(lo, big.NewInt(math.MinInt64)), k, exLo && lo.Cmp(big.NewInt(math.MinInt64)) >= 0)
	last := floorDiv(minInt(hi, big.NewInt(math.MaxInt64)), k, exHi && hi.Cmp(big.NewInt(math.MaxInt64)) <= 0)

	return first.Cmp(last) <= 0
}

// decimalExists reports whether a multiple of step, any number when step is "", lies between lo and hi, each end
// excluded when its flag says so.
func decimalExists(lo, hi *big.Rat, exLo, exHi bool, step string) bool {
	if step == "" {
		return lo.Cmp(hi) < 0 || !exLo && !exHi && lo.Cmp(hi) == 0
	}

	m, _ := new(big.Rat).SetString(step)
	ql, qh := new(big.Rat).Quo(lo, m), new(big.Rat).Quo(hi, m)

	// The quotients have denominators above 0: n / d rounded down is their Euclidean quotient.
	first := ceilDiv(ql.Num(), ql.Denom(), exLo)
	last := floorDiv(qh.Num(), qh.Denom(), exHi)

	return first.Cmp(last) <= 0
}

// ceilDiv returns the least k whose k × d is at least n, or above it when exclusive.
func ceilDiv(n, d *big.Int, exclusive bool) *big.Int {
	q, m := new(big.Int).DivMod(n, d, new(big.Int))
	if m.Sign() != 0 || exclusive {
		q.Add(q, big.NewInt(1))
	}

	return q
}

// floorDiv returns the greatest k whose k × d is at most n, or below it when exclusive.
func floorDiv(n, d *big.Int, exclusive bool) *big.Int {
	q, m := new(big.Int).DivMod(n, d, new(big.Int))
	if m.Sign() == 0 && exclusive {
		q.Sub(q, big.NewInt(1))
	}

	return q
}

// minInt returns the lesser of a and b.
func minInt(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return a
	}

	return b
}

// maxInt returns the greater of a and b.
func maxInt(a, b *big.Int) *big.Int {
	if a.Cmp(b) >= 0 {
		return a
	}

	return b
}

// Generators

// rootGen draws the schemas of gen, or one of them behind a $ref to the $defs of the document.
func rootGen(gen *rapid.Generator[map[string]any]) *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		s := gen.Draw(t, "schema")
		if !rapid.Bool().Draw(t, "ref") {
			return s
		}

		return map[string]any{"$defs": map[string]any{"S": s}, "$ref": "#/$defs/S"}
	})
}

// schemaGen draws JSON Schemas whose values fakefy must make: every keyword it reads, with bounds that some value
// meets, nested up to depth levels.
func schemaGen(depth int) *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		kinds := []string{
			"string", "pattern", "format and pattern", "integer", "big integer", "exclusive integer", "allOf", "number",
			"decimal", "boolean", "null", "enum", "const", "nullable", "oneOf",
		}
		if depth > 0 {
			kinds = append(kinds, "object", "pattern object", "array", "unique array", "tuple", "anyOf", "allOf objects")
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
				"pattern":   rapid.SampledFrom([]string{"^[a-z]+$", "^[0-9a-f]*$", "^[A-Z][a-z]+$", "^(ab|c)+-?[0-9]*$", "[0-9]", "x$", "^a"}).Draw(t, "pattern"),
				"minLength": lo,
				"maxLength": lo + rapid.IntRange(0, 4).Draw(t, "extra"),
			}
		case "format and pattern":
			return rapid.SampledFrom([]map[string]any{
				{"type": "string", "format": "email", "pattern": "^x"},
				{"type": "string", "format": "email", "pattern": "@"},
				{"type": "string", "format": "hostname", "pattern": "\\.io$"},
				{"type": "string", "format": "uuid", "pattern": "^[0-9a-f-]+$"},
				{"type": "string", "format": "date", "pattern": "^20"},
				{"type": "string", "format": "uri", "pattern": "^https://"},
			}).Draw(t, "schema")
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
		case "big integer":
			lo := rapid.Int64Range(math.MinInt64, math.MaxInt64-1000).Draw(t, "minimum")
			return map[string]any{"type": "integer", "minimum": lo, "maximum": lo + rapid.Int64Range(0, 1000).Draw(t, "width")}
		case "exclusive integer":
			lo := rapid.IntRange(-1000, 1000).Draw(t, "exclusiveMinimum")
			return map[string]any{"type": "integer", "exclusiveMinimum": lo, "exclusiveMaximum": lo + rapid.IntRange(2, 50).Draw(t, "width")}
		case "number":
			lo := rapid.Float64Range(-1e6, 1e6).Draw(t, "minimum")
			return map[string]any{"type": "number", "minimum": lo, "exclusiveMaximum": lo + rapid.Float64Range(0.5, 100).Draw(t, "width")}
		case "decimal":
			step := rapid.SampledFrom([]float64{0.01, 0.05, 0.25, 0.5, 1.5}).Draw(t, "multipleOf")
			lo := float64(rapid.IntRange(-100000, 100000).Draw(t, "minimum")) / 100

			return map[string]any{"type": "number", "minimum": lo, "maximum": lo + 2*step + float64(rapid.IntRange(0, 1000).Draw(t, "width")), "multipleOf": step}
		case "boolean":
			return map[string]any{"type": "boolean"}
		case "null":
			return map[string]any{"type": "null"}
		case "enum":
			return map[string]any{"enum": []any{rapid.String().Draw(t, "a"), rapid.Int().Draw(t, "b")}}
		case "const":
			return map[string]any{"const": rapid.SampledFrom([]any{"c", 7, true, nil, 2.5, []any{1, "a"}, map[string]any{"k": "v"}}).Draw(t, "const")}
		case "nullable":
			// The bounds may cross: then only null is left.
			return map[string]any{"type": []any{"integer", "null"}, "minimum": rapid.IntRange(0, 10).Draw(t, "minimum"), "maximum": rapid.IntRange(0, 10).Draw(t, "maximum")}
		case "oneOf":
			// Branches of different types: a value matches one only.
			branches := rapid.Permutation([]any{
				map[string]any{"type": "string", "maxLength": 3},
				map[string]any{"type": "boolean"},
				map[string]any{"type": "null"},
				map[string]any{"type": "array", "maxItems": 0},
			}).Draw(t, "branches")

			return map[string]any{"oneOf": branches[:rapid.IntRange(1, len(branches)).Draw(t, "count")]}
		case "anyOf":
			return map[string]any{"anyOf": []any{schemaGen(depth-1).Draw(t, "a"), schemaGen(depth-1).Draw(t, "b")}}
		case "object":
			return objectGen(depth, "", false).Draw(t, "object")
		case "pattern object":
			return objectGen(depth, "", true).Draw(t, "object")
		case "allOf objects":
			// Each side names its own properties: one name of two unrelated schemas would have no value.
			a, b := objectGen(depth, "a_", false).Draw(t, "a"), objectGen(depth, "b_", false).Draw(t, "b")
			delete(a, "additionalProperties")
			delete(b, "additionalProperties")
			delete(a, "maxProperties")
			delete(b, "maxProperties")

			return map[string]any{"allOf": []any{a, b}}
		case "unique array":
			lo := rapid.IntRange(0, 5).Draw(t, "minItems")
			items := rapid.SampledFrom([]map[string]any{
				{"type": "integer", "minimum": 0, "maximum": 1000},
				{"type": "string", "minLength": 3},
				{"type": "integer", "minimum": 9007199254740992, "maximum": 9007199254741100},
				{"enum": []any{"a", "b", "c", "d", "e", 1, 2, 3}},
			}).Draw(t, "items")

			return map[string]any{"type": "array", "uniqueItems": true, "items": items, "minItems": lo, "maxItems": lo + rapid.IntRange(0, 3).Draw(t, "extra")}
		case "tuple":
			n := rapid.IntRange(1, 3).Draw(t, "prefixItems")
			prefix := make([]any, n)

			for i := range prefix {
				prefix[i] = schemaGen(depth-1).Draw(t, fmt.Sprintf("prefix%d", i))
			}

			s := map[string]any{"type": "array", "prefixItems": prefix, "minItems": rapid.IntRange(0, n).Draw(t, "minItems")}
			if rapid.Bool().Draw(t, "closed") {
				s["items"] = false
			}

			return s
		default:
			lo := rapid.IntRange(0, 2).Draw(t, "minItems")
			return map[string]any{"type": "array", "items": schemaGen(depth-1).Draw(t, "items"), "minItems": lo, "maxItems": lo + rapid.IntRange(0, 3).Draw(t, "extra")}
		}
	})
}

// objectGen draws closed objects of up to three properties of schemaGen, depth levels down, their names starting with
// prefix and some of them required, with minProperties and maxProperties they meet. With patterns, it adds required
// properties only patternProperties allow.
func objectGen(depth int, prefix string, patterns bool) *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		props := make(map[string]any)
		var required []any

		for i := range rapid.IntRange(0, 3).Draw(t, "properties") {
			name := prefix + fmt.Sprintf("%s%d", rapid.SampledFrom([]string{"id", "email", "name", "createdAt", "price", "age", "url", "x"}).Draw(t, "name"), i)
			props[name] = schemaGen(depth-1).Draw(t, name)

			if rapid.Bool().Draw(t, "required") {
				required = append(required, name)
			}
		}

		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}

		if patterns {
			s["patternProperties"] = map[string]any{"^p_": schemaGen(depth-1).Draw(t, "pattern")}
			for i := range rapid.IntRange(1, 2).Draw(t, "pattern properties") {
				required = append(required, fmt.Sprintf("p_%d", i))
			}
		}

		if len(required) > 0 {
			s["required"] = required
		}

		total := len(props) + len(required)
		for _, r := range required {
			if _, ok := props[r.(string)]; ok {
				total--
			}
		}

		lo := rapid.IntRange(0, total).Draw(t, "minProperties")
		if lo > 0 {
			s["minProperties"] = lo
		}

		if rapid.Bool().Draw(t, "bounded") {
			s["maxProperties"] = max(lo, len(required)) + rapid.IntRange(0, 2).Draw(t, "extra")
		}

		return s
	})
}

// anySchemaGen draws schemas of the keywords fakefy reads, with values of any kind, nested up to depth levels: bounds
// that cross, keywords that contradict, counts that aren't counts, patterns that don't compile and refs to nothing.
func anySchemaGen(depth int) *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		s := make(map[string]any)
		keys := []string{
			"type", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength",
			"pattern", "format", "enum", "const", "example", "minItems", "maxItems", "uniqueItems", "required",
			"additionalProperties", "minProperties", "maxProperties", "$ref",
		}
		if depth > 0 {
			keys = append(keys, "items", "prefixItems", "properties", "patternProperties", "allOf", "anyOf", "oneOf")
		}

		number := rapid.SampledFrom([]any{0, 1, -1, 3, 2.5, 0.01, -7.5, 1e300, -1e300, 1e-300, 9007199254740993, math.MaxInt64, "5", true})
		count := rapid.SampledFrom([]any{0, 1, 2, 5, -1, 1.5, "2"})
		sub := func(label string) any {
			if rapid.IntRange(0, 5).Draw(t, label+" bool") == 0 {
				return rapid.Bool().Draw(t, label)
			}

			return anySchemaGen(depth-1).Draw(t, label)
		}

		for range rapid.IntRange(0, 4).Draw(t, "keywords") {
			switch key := rapid.SampledFrom(keys).Draw(t, "key"); key {
			case "type":
				types := []any{"string", "integer", "number", "boolean", "null", "object", "array"}
				if rapid.Bool().Draw(t, "type list") {
					s[key] = rapid.SliceOfNDistinct(rapid.SampledFrom(types), 1, 3, func(v any) any { return v }).Draw(t, key)
				} else {
					s[key] = rapid.SampledFrom(types).Draw(t, key)
				}
			case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
				s[key] = number.Draw(t, key)
			case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
				s[key] = count.Draw(t, key)
			case "pattern":
				s[key] = rapid.SampledFrom([]string{"^a+$", "[0-9]", "^$", "(", "^.{3}$", "x$"}).Draw(t, key)
			case "format":
				s[key] = rapid.SampledFrom([]string{"email", "uuid", "date-time", "int32", "hostname"}).Draw(t, key)
			case "enum":
				s[key] = rapid.SliceOfN(rapid.SampledFrom([]any{"a", 1, 1.0, nil, true, []any{}}), 0, 3).Draw(t, key)
			case "const", "example":
				s[key] = rapid.SampledFrom([]any{"a", 1, nil, false, map[string]any{"a": 1}, []any{1, 1}}).Draw(t, key)
			case "uniqueItems":
				s[key] = rapid.Bool().Draw(t, key)
			case "required":
				s[key] = rapid.SliceOfN(rapid.SampledFrom([]any{"a", "b", "c"}), 0, 3).Draw(t, key)
			case "$ref":
				s[key] = rapid.SampledFrom([]string{"#", "#/properties/a", "#/missing", "other.json"}).Draw(t, key)
			case "additionalProperties", "items":
				s[key] = sub(key)
			case "prefixItems", "allOf", "anyOf", "oneOf":
				list := make([]any, rapid.IntRange(0, 2).Draw(t, key+" length"))
				for i := range list {
					list[i] = sub(fmt.Sprintf("%s%d", key, i))
				}

				s[key] = list
			case "properties", "patternProperties":
				names := []string{"a", "b", "c"}
				if key == "patternProperties" {
					names = []string{"^a", "b$", "("}
				}

				props := make(map[string]any)
				for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom(names), 0, 2, func(v string) string { return v }).Draw(t, key) {
					props[name] = sub(name)
				}

				s[key] = props
			}
		}

		return s
	})
}

// bigIntGen draws integers across the range of an int64 and a little past both ends.
func bigIntGen() *rapid.Generator[*big.Int] {
	return rapid.Custom(func(t *rapid.T) *big.Int {
		base := rapid.SampledFrom([]*big.Int{
			big.NewInt(0), big.NewInt(1 << 53), big.NewInt(-(1 << 53)), big.NewInt(math.MaxInt64), big.NewInt(math.MinInt64),
		}).Draw(t, "base")

		return new(big.Int).Add(base, big.NewInt(rapid.Int64Range(-2_000_000, 2_000_000).Draw(t, "offset")))
	})
}

// decimalGen draws decimals of up to four places, up to a billion.
func decimalGen() *rapid.Generator[*big.Rat] {
	return rapid.Custom(func(t *rapid.T) *big.Rat {
		return big.NewRat(rapid.Int64Range(-1e13, 1e13).Draw(t, "ten-thousandths"), 10000)
	})
}
