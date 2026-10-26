package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

const _petSchema = `{
	"type": "object",
	"required": ["name"],
	"properties": {
		"name": {"type": "string"},
		"age": {"type": "integer", "minimum": 0},
		"owner": {"type": "string", "format": "email"}
	}
}`

func schemaMatches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

func TestJSONSchema_MatchesWhatConforms(t *testing.T) {
	m := JSONSchema(_petSchema)

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"body", `{"name": "Rex", "age": 3}`, true},
		{"bytes", []byte(`{"name": "Rex"}`), true},
		{"Go value", map[string]any{"name": "Rex", "age": 3}, true},
		{"whole float is an integer", `{"name": "Rex", "age": 3.0}`, true},
		{"missing required", `{"age": 3}`, false},
		{"wrong type", `{"name": 1}`, false},
		{"below the minimum", `{"name": "Rex", "age": -1}`, false},
		{"not an integer", `{"name": "Rex", "age": 1.5}`, false},
		{"format is checked", `{"name": "Rex", "owner": "not an email"}`, false},
		{"format", `{"name": "Rex", "owner": "ann@example.com"}`, true},
		{"not JSON", `{"name": `, false},
		{"nil", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, schemaMatches(t, m, tc.value))
		})
	}
}

func TestJSONSchema_MatchesDecodedValues(t *testing.T) {
	// Inside other matchers, such as Field, the value is a decoded JSON value: a number is a number. Text is read as
	// JSON, as EqualJSON reads it.
	m := JSONSchema(`{"type": "integer"}`)
	require.True(t, schemaMatches(t, m, int64(3)))
	require.True(t, schemaMatches(t, m, "3"))
	require.False(t, schemaMatches(t, m, 1.5))

	require.True(t, schemaMatches(t, Field("$.age", m), `{"age": 3}`))
	require.False(t, schemaMatches(t, Field("$.age", m), `{"age": 3.5}`))
}

func TestJSONSchema_Draft(t *testing.T) {
	// draft-04 writes exclusiveMaximum as a bool; 2020-12, the default, as a number.
	const draft4 = `{"type": "integer", "maximum": 10, "exclusiveMaximum": true}`

	m := JSONSchema(draft4, SchemaDraft(JSONSchemaDraft4))
	require.True(t, schemaMatches(t, m, int64(9)))
	require.False(t, schemaMatches(t, m, int64(10)))

	require.Panics(t, func() { JSONSchema(draft4) })

	// $schema wins over the default.
	named := JSONSchema(`{"$schema": "http://json-schema.org/draft-04/schema#", "maximum": 10, "exclusiveMaximum": true}`)
	require.False(t, schemaMatches(t, named, int64(10)))
}

func TestJSONSchema_LoadsNothingOutsideTheSchema(t *testing.T) {
	// A mock file mustn't make the server read its files or fetch from the network.
	for _, ref := range []string{"file:///etc/passwd", "https://example.com/schema.json", "other.json"} {
		err, ok := panicOf(func() { JSONSchema(`{"$ref": "` + ref + `"}`) }).(error)
		require.True(t, ok, ref)
		require.ErrorContains(t, err, errSchemaLoad.Error(), ref)
	}

	local := JSONSchema(`{"$defs": {"name": {"type": "string"}}, "properties": {"name": {"$ref": "#/$defs/name"}}}`)
	require.True(t, schemaMatches(t, local, `{"name": "Rex"}`))
	require.False(t, schemaMatches(t, local, `{"name": 1}`))
}

func TestJSONSchema_PanicsOnWhatIsntASchema(t *testing.T) {
	for _, schema := range []string{`{"type": `, `{"type": "nope"}`, `{"minimum": "1"}`} {
		err, ok := panicOf(func() { JSONSchema(schema) }).(error)
		require.True(t, ok, schema)
		require.ErrorContains(t, err, "match: jsonschema: ", schema)
	}

	err, ok := panicOf(func() { JSONSchema(`{}`, SchemaDraft(JSONSchemaDraft(99))) }).(error)
	require.True(t, ok)
	require.EqualError(t, err, "match: jsonschema: unknown draft JSONSchemaDraft(99)")
}

func TestJSONSchema_Describes(t *testing.T) {
	data, err := JSONSchema(`{"type": "string"}`).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["jsonSchema", "{\"type\": \"string\"}"]`, string(data))

	data, err = JSONSchema(`{}`, SchemaDraft(JSONSchemaDraft2019)).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["jsonSchema", "{}", {"draft": "2019-09"}]`, string(data))
}
