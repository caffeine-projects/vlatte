package openapiv3

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeref_GivesNoValueForAReferenceItCannotFollow(t *testing.T) {
	// kin-openapi follows the same references first, so these never reach the reader from Load: should one, the
	// reader leaves what kin-openapi read, rather than looping or failing.
	spec := &url.URL{Path: "/specs/spec.json"}
	r := &rawReader{
		data: map[string][]byte{
			"/specs/spec.json": []byte(`{"a": {"$ref": "#/b"}, "b": {"$ref": "#/a"}, "c": {"$ref": "%zz"}}`),
		},
		files: make(map[string]any),
		out:   make(raw),
	}
	doc := node{file: spec, v: r.file(spec)}

	tests := []struct {
		name string
		key  string
	}{
		{name: "a cycle", key: "a"},
		{name: "not a URL", key: "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, node{}, r.deref(r.child(doc, tt.key)))
		})
	}
}

func TestResolved_KeepsOnlyTheKeywordsBesideAReferenceItCannotFollow(t *testing.T) {
	// As deref, for a schema as kin-openapi resolves it in OpenAPI 3.1: a cycle resolves to no schema, and the
	// keywords beside a reference that isn't a URL are all there is of it.
	spec := &url.URL{Path: "/specs/spec.json"}
	r := &rawReader{
		v31: true,
		data: map[string][]byte{
			"/specs/spec.json": []byte(`{"a": {"$ref": "#/b"}, "b": {"$ref": "#/a"}, "c": {"$ref": "%zz", "description": "c"}}`),
		},
		files: make(map[string]any),
		out:   make(raw),
	}
	doc := node{file: spec, v: r.file(spec)}

	require.Nil(t, r.resolved(r.child(doc, "a"), _maxRefs))
	require.Equal(t, node{}, r.annotated(r.child(doc, "a"), "example"))
	require.Equal(t, map[string]any{"description": "c"}, r.resolved(r.child(doc, "c"), _maxRefs))
}
