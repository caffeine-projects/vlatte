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
