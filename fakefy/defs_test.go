package fakefy_test

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/fakefy"
)

func TestWithDefs_ChecksEachValueAgainstTheSchemasItsRefsReach(t *testing.T) {
	// The values are those of the schema with the shared schemas it reaches under its $defs: checked against every
	// one, so an example that breaks its schema never makes a mock wrong. checked is such a schema: unchecked, its
	// value is the example 0, checked, the enum's 5.
	checked := map[string]any{"type": "integer", "minimum": 1, "example": 0, "enum": []any{5}}

	tests := []struct {
		name   string
		shared map[string]any
		schema map[string]any
		want   any
	}{
		{
			name:   "a $ref to a shared schema",
			shared: map[string]any{"N": checked},
			schema: map[string]any{"$ref": "#/$defs/N"},
			want:   int64(5),
		},
		{
			name:   "a shared schema that names another",
			shared: map[string]any{"P": map[string]any{"$ref": "#/$defs/N"}, "N": checked},
			schema: map[string]any{"$ref": "#/$defs/P"},
			want:   int64(5),
		},
		{
			name:   "a $ref into a shared schema",
			shared: map[string]any{"P": map[string]any{"properties": map[string]any{"n": checked}}},
			schema: map[string]any{"$ref": "#/$defs/P/properties/n"},
			want:   int64(5),
		},
		{
			name:   "a name a $ref escapes",
			shared: map[string]any{"a/b ~%": checked},
			schema: map[string]any{"$ref": "#/$defs/a~1b%20~0%25"},
			want:   int64(5),
		},
		{
			// A shared schema's $ref to a name the schema's own $defs hold resolves to the schema's own.
			name: "own $defs over shared schemas",
			shared: map[string]any{
				"P": map[string]any{
					"type": "object", "required": []any{"t"},
					"properties": map[string]any{"t": map[string]any{"$ref": "#/$defs/T"}},
				},
				"T": map[string]any{"type": "integer"},
			},
			schema: map[string]any{"$ref": "#/$defs/P", "$defs": map[string]any{"T": map[string]any{"const": "own"}}},
			want:   map[string]any{"t": "own"},
		},
		{
			// Draft 4 has no if: the example holds.
			name: "a $schema of another draft",
			shared: map[string]any{"N": map[string]any{
				"type": "integer", "example": 4, "if": map[string]any{"minimum": 4}, "then": map[string]any{"maximum": 3},
			}},
			schema: map[string]any{"$schema": "http://json-schema.org/draft-04/schema#", "$ref": "#/$defs/N"},
			want:   int64(4),
		},
		{
			// A shared schema it doesn't reach leaves its own checked.
			name:   "a shared schema that is not one",
			shared: map[string]any{"N": checked, "Bad": map[string]any{"type": 5}},
			schema: map[string]any{"$ref": "#/$defs/N"},
			want:   int64(5),
		},
		{
			name:   "a schema that names no shared schema",
			shared: map[string]any{"N": map[string]any{"type": "string"}},
			schema: checked,
			want:   int64(5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := fakefy.New(fakefy.WithSeed(1), fakefy.WithDefs(fakefy.NewDefs(tt.shared))).Schema(tt.schema)
			require.NoError(t, err)
			require.Equal(t, tt.want, v)
		})
	}
}

func TestWithDefs_ASchemaWithAnIDReadsNoFile(t *testing.T) {
	// A $id moves where the $refs of a schema resolve, but not out of it: the shared schemas a value is checked against
	// are those of Defs, never a file where the $id points.
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "defs.json"), []byte(`{"$defs": {"N": {"type": "string"}}}`), 0o600))
	t.Chdir(dir)

	shared := map[string]any{"N": map[string]any{"type": "integer", "minimum": 1, "example": 0, "enum": []any{5}}}

	v, err := fakefy.New(fakefy.WithSeed(1), fakefy.WithDefs(fakefy.NewDefs(shared))).Schema(map[string]any{"$id": "sub/pet.json", "$ref": "#/$defs/N"})
	require.NoError(t, err)
	require.Equal(t, int64(5), v)
}

func TestWithDefs_MakesTheValuesOfTheSchemaWithTheSchemasItReachesUnderItsDefs(t *testing.T) {
	// Defs only saves time: a Faker of a seed makes the same values with it as with the schemas in each schema.
	pet := map[string]any{
		"type": "object", "required": []any{"id", "name", "tags"},
		"properties": map[string]any{
			"id":    map[string]any{"type": "string", "format": "uuid"},
			"name":  map[string]any{"type": "string", "minLength": 2},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Tag"}, "minItems": 1},
			"owner": map[string]any{"$ref": "#/$defs/Owner"},
		},
	}
	tag := map[string]any{"type": "string", "enum": []any{"a", "b", "c"}}
	owner := map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string", "format": "email"}}}
	checked := map[string]any{"type": "integer", "minimum": 1, "example": 0, "enum": []any{5}}

	// root holds either an object whose c holds root again, or an integer: R refers to it.
	root := map[string]any{"anyOf": []any{
		map[string]any{"type": "object", "required": []any{"c"}, "properties": map[string]any{"c": map[string]any{"$ref": "#/$defs/R"}}},
		map[string]any{"type": "integer", "minimum": 1, "example": 0},
	}}
	toRoot := map[string]any{"allOf": []any{map[string]any{"$ref": "#"}}}

	// P's child applies the root through $dynamicRef: an example whose child lacks id breaks it.
	dynamic := map[string]any{
		"type": "object", "required": []any{"id"}, "examples": []any{map[string]any{"id": 1, "child": make(map[string]any)}},
		"properties": map[string]any{
			"id":    map[string]any{"type": "integer"},
			"child": map[string]any{"$dynamicRef": "#", "type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}},
		},
	}

	// A Go caller's schema can hold what JSON can't: its values go unchecked, with Defs or without.
	notJSON := map[string]any{"type": "integer", "minimum": 1, "example": 0, "x-limit": math.Inf(1)}

	// X is a resource of its own that names itself defs.json and holds an N of its own.
	own := map[string]any{"X": map[string]any{"$id": "defs.json", "$defs": map[string]any{"N": map[string]any{"description": "x"}}}}

	tests := []struct {
		name     string
		shared   map[string]any
		schema   map[string]any
		embedded map[string]any
	}{
		{
			name:     "shared schemas that name others",
			shared:   map[string]any{"Pet": pet, "Tag": tag, "Owner": owner, "Unused": map[string]any{"type": "integer"}},
			schema:   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Pet"}, "minItems": 2},
			embedded: map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Pet"}, "minItems": 2, "$defs": map[string]any{"Pet": pet, "Tag": tag, "Owner": owner}},
		},
		{
			// # is the root of the value's schema, not of the shared ones.
			name:     "a shared schema that refers to the root",
			shared:   map[string]any{"R": toRoot},
			schema:   root,
			embedded: map[string]any{"anyOf": root["anyOf"], "$defs": map[string]any{"R": toRoot}},
		},
		{
			// # is the root of the value's schema here too.
			name:     "a shared schema with a $dynamicRef",
			shared:   map[string]any{"P": dynamic},
			schema:   map[string]any{"$ref": "#/$defs/P"},
			embedded: map[string]any{"$ref": "#/$defs/P", "$defs": map[string]any{"P": dynamic}},
		},
		{
			name:     "a $id below the root",
			shared:   map[string]any{"N": checked},
			schema:   map[string]any{"$ref": "#/$defs/N", "$defs": own},
			embedded: map[string]any{"$ref": "#/$defs/N", "$defs": map[string]any{"X": own["X"], "N": checked}},
		},
		{
			name:     "a schema that is not JSON",
			shared:   map[string]any{"N": checked},
			schema:   map[string]any{"$ref": "#/$defs/N", "x-limit": math.Inf(1)},
			embedded: map[string]any{"$ref": "#/$defs/N", "x-limit": math.Inf(1), "$defs": map[string]any{"N": checked}},
		},
		{
			name:     "a shared schema that is not JSON",
			shared:   map[string]any{"N": notJSON},
			schema:   map[string]any{"$ref": "#/$defs/N"},
			embedded: map[string]any{"$ref": "#/$defs/N", "$defs": map[string]any{"N": notJSON}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			with := fakefy.New(fakefy.WithSeed(3), fakefy.WithDefs(fakefy.NewDefs(tt.shared)))
			without := fakefy.New(fakefy.WithSeed(3))

			for range _seeds {
				got, err := with.Schema(tt.schema)
				require.NoError(t, err)

				want, err := without.Schema(tt.embedded)
				require.NoError(t, err)

				require.Equal(t, want, got)
			}
		})
	}
}

func TestWithDefs_FailsOnARefToNoSchemaAsWithout(t *testing.T) {
	schema := map[string]any{"$ref": "#/$defs/M", "$defs": map[string]any{"O": make(map[string]any)}}

	_, err := fakefy.New(fakefy.WithDefs(fakefy.NewDefs(map[string]any{"N": make(map[string]any)}))).Schema(schema)
	require.ErrorIs(t, err, fakefy.ErrUnresolved)
	require.EqualError(t, err, `fakefy: unresolved $ref: at the root: "#/$defs/M": no member "M"`)
}

func TestDefs_IsSafeForFakersOnManyGoroutines(t *testing.T) {
	// A generator may make the values of many schemas at once: each of its Fakers makes the values it makes alone.
	shared := map[string]any{
		"Pet": map[string]any{"type": "object", "required": []any{"id"}, "properties": map[string]any{"id": map[string]any{"$ref": "#/$defs/ID"}}},
		"ID":  map[string]any{"type": "integer", "minimum": 1, "example": 0},
	}

	schemas := []map[string]any{
		{"$ref": "#/$defs/Pet"},
		{"type": "array", "items": map[string]any{"$ref": "#/$defs/Pet"}},
		{"$ref": "#/$defs/ID"},
	}

	want := make([]any, 8)
	for i := range want {
		v, err := fakefy.New(fakefy.WithSeed(uint64(i)), fakefy.WithDefs(fakefy.NewDefs(shared))).Schema(schemas[i%len(schemas)])
		require.NoError(t, err)

		want[i] = v
	}

	defs := fakefy.NewDefs(shared)
	got := make([]any, len(want))
	errs := make([]error, len(want))

	var wg sync.WaitGroup
	for i := range want {
		wg.Go(func() {
			got[i], errs[i] = fakefy.New(fakefy.WithSeed(uint64(i)), fakefy.WithDefs(defs)).Schema(schemas[i%len(schemas)])
		})
	}

	wg.Wait()

	require.Equal(t, make([]error, len(want)), errs)
	require.Equal(t, want, got)
}
