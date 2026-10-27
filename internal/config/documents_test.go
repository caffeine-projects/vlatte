package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocuments_SplitsAYAMLStream(t *testing.T) {
	// Each document of a YAML stream is a mock of its own, so none may be dropped or merged with another. Each keeps the
	// lines it has in the file, so a parse error points to the line a person sees in their editor.
	for _, tc := range []struct {
		name string
		data string
		want []string
	}{
		{
			name: "one document",
			data: "id: a\n",
			want: []string{"id: a\n"},
		},
		{
			name: "one document after a marker",
			data: "# mocks\n---\nid: a\n---\n",
			want: []string{"# mocks\n---\nid: a\n"},
		},
		{
			name: "empty",
			data: "",
			want: []string{""},
		},
		{
			name: "two documents",
			data: "id: a\n---\nid: b\n",
			want: []string{"id: a\n", "\n---\nid: b\n"},
		},
		{
			name: "one document after an empty one",
			data: "---\n---\nid: a\n",
			want: []string{"\n---\nid: a\n"},
		},
		{
			name: "leading, repeated and trailing markers",
			data: "---\n---\nid: a\n--- # b\nid: b\n---\n",
			want: []string{"\n---\nid: a\n", "\n\n\n--- # b\nid: b\n"},
		},
		{
			name: "content on the marker line",
			data: "--- {id: a}\n--- {id: b}\n",
			want: []string{"--- {id: a}\n", "\n--- {id: b}\n"},
		},
		{
			name: "directives belong to the first document",
			data: "%YAML 1.2\n---\nid: a\n---\nid: b",
			want: []string{"%YAML 1.2\n---\nid: a\n", "\n\n\n---\nid: b"},
		},
		{
			name: "document end markers",
			data: "id: a\n...\nid: b\n...\n# end\n",
			want: []string{"id: a\n...\n", "\n\nid: b\n...\n"},
		},
		{
			name: "windows line endings",
			data: "id: a\r\n---\r\nid: b\r\n",
			want: []string{"id: a\r\n", "\n---\r\nid: b\r\n"},
		},
		{
			name: "one document after empty ones",
			data: "---\n...\n---\nid: a\n",
			want: []string{"\n\n---\nid: a\n"},
		},
		{
			name: "a byte order mark is not content",
			data: "\xEF\xBB\xBF# mocks\n---\nid: a\n---\nid: b\n",
			want: []string{"\xEF\xBB\xBF# mocks\n---\nid: a\n", "\n\n\n---\nid: b\n"},
		},
		{
			name: "a marker needs a space or the line end after it",
			data: "a: |\n  ---\n---text\n",
			want: []string{"a: |\n  ---\n---text\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Documents([]byte(tc.data), "yaml")

			want := make([][]byte, 0, len(tc.want))
			for _, w := range tc.want {
				want = append(want, []byte(w))
			}

			require.Equal(t, want, got)
		})
	}
}

func TestDocuments_EveryDocumentDecodesAsItsOwn(t *testing.T) {
	// Splitting must agree with the YAML parser: each piece decodes to the document it was in the stream.
	docs := Documents([]byte("---\nid: a\nbody: |\n  ---not a marker\n--- {id: b}\n...\n---\nid: c\n"), "yml")
	require.Len(t, docs, 3)

	ids := make([]any, 0, len(docs))
	for _, doc := range docs {
		v, err := Parse(doc, "yaml")
		require.NoError(t, err)

		ids = append(ids, v.(map[string]any)["id"])
	}

	require.Equal(t, []any{"a", "b", "c"}, ids)
}

func TestDocuments_ASyntaxErrorKeepsItsLine(t *testing.T) {
	docs := Documents([]byte("id: a\n---\nid: b\n---\nid: [\n"), "yaml")
	require.Len(t, docs, 3)

	_, err := Parse(docs[2], "yaml")
	require.ErrorContains(t, err, "yaml: line 5:")
}

func TestDocuments_JSONIsWhole(t *testing.T) {
	data := []byte("{\"id\": \"---\"}\n---\n")
	require.Equal(t, [][]byte{data}, Documents(data, ".JSON"))
}
