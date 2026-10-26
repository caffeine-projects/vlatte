package vlatte

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "list without items is an empty JSON list", text: `{{ list | toJSON }}`, want: "[]"},
		{name: "list", text: `{{ list 1 "a" | toJSON }}`, want: `[1,"a"]`},
		{name: "append", text: `{{ append (list 1 2) 3 | toJSON }}`, want: "[1,2,3]"},
		{name: "append to nil", text: `{{ append nil 1 | toJSON }}`, want: "[1]"},
		{name: "append to a value that isn't a list", text: `{{ append "a" 1 }}`, err: "string is not a list"},
		{name: "insert at the start", text: `{{ insert (list 1 2) 0 0 | toJSON }}`, want: "[0,1,2]"},
		{name: "insert at the end", text: `{{ insert (list 1 2) 2 3 | toJSON }}`, want: "[1,2,3]"},
		{name: "insert out of range", text: `{{ insert (list 1) 3 0 }}`, err: "index 3 is out of [0:1]"},
		{name: "remove by index", text: `{{ remove (list 1 2 3) 1 | toJSON }}`, want: "[1,3]"},
		{name: "remove the last item", text: `{{ remove (list 1 2 3) -1 | toJSON }}`, want: "[1,2]"},
		{name: "remove out of range", text: `{{ remove (list 1) -2 }}`, err: "index -2 is out of a list of 1 items"},
		{name: "reverse", text: `{{ reverse (list 1 2 3) | toJSON }}`, want: "[3,2,1]"},
		{name: "randomItem of an empty list", text: `{{ randomItem list }}`, err: "the list is empty"},
		{name: "insert into a value that isn't a list", text: `{{ insert "a" 0 1 }}`, err: "error calling insert: string is not a list"},
		{name: "remove from a value that isn't a list", text: `{{ remove "a" 0 }}`, err: "error calling remove: string is not a list"},
		{name: "randomItem of a value that isn't a list", text: `{{ randomItem "a" }}`, err: "error calling randomItem: string is not a list"},
		{name: "shuffle a value that isn't a list", text: `{{ shuffle "a" }}`, err: "error calling shuffle: string is not a list"},
		{name: "reverse a value that isn't a list", text: `{{ reverse "a" }}`, err: "error calling reverse: string is not a list"},
	})
}

func TestListFuncs_LeaveTheirListUnchanged(t *testing.T) {
	// Templates read the data of every request: a function that changed a list would change it for the next one.
	data := map[string]any{"items": []any{"a", "b", "c"}}

	out, err := render(t, MustNewGoTemplateEngine(),
		`{{ $l := .items }}{{ append $l "d" | len }}{{ insert $l 0 "z" | len }}{{ remove $l 0 | len }}`+
			`{{ reverse $l | len }}{{ shuffle $l | len }}{{ sortBy $l "$" | len }}`, data)
	require.NoError(t, err)
	require.Equal(t, "442333", out)
	require.Equal(t, []any{"a", "b", "c"}, data["items"])
}

func TestRandomListFuncs(t *testing.T) {
	e := MustNewGoTemplateEngine()

	t.Run("randomItem is an item of the list", func(t *testing.T) {
		out, err := render(t, e, `{{ randomItem (list "a" "b" "c") }}`, nil)
		require.NoError(t, err)
		require.Contains(t, []string{"a", "b", "c"}, out)
	})

	t.Run("shuffle keeps every item, once", func(t *testing.T) {
		out, err := render(t, e, `{{ shuffle (list "a" "b" "c" "d") | join "," }}`, nil)
		require.NoError(t, err)

		got := strings.Split(out, ",")
		slices.Sort(got)
		require.Equal(t, []string{"a", "b", "c", "d"}, got)
	})
}

func TestSortBy(t *testing.T) {
	people := `[{"name": "c", "age": 30}, {"name": "b", "age": 20}, {"name": "n"}, {"name": "a", "age": 20}]`

	runFuncCases(t, []funcCase{
		{
			// Stable: b and a have the same age and keep their order. An item without the field comes first.
			name: "by a number, stable, missing values first", text: `{{ range sortBy (fromJSON .) "$.age" }}{{ .name }}{{ end }}`,
			data: people, want: "nbac",
		},
		{name: "by a string", text: `{{ range sortBy (fromJSON .) "$.name" }}{{ .name }}{{ end }}`, data: people, want: "abcn"},
		{name: "descending with reverse", text: `{{ range reverse (sortBy (fromJSON .) "$.age") }}{{ .name }}{{ end }}`, data: people, want: "cabn"},
		{name: "plain values with $", text: `{{ sortBy (list 3 1.5 2) "$" | toJSON }}`, want: "[1.5,2,3]"},
		{name: "strings are values, not JSON text", text: `{{ sortBy (list "b" "a") "$" | join "" }}`, want: "ab"},
		{name: "booleans, false first", text: `{{ sortBy (list true false true) "$" | toJSON }}`, want: "[false,true,true]"},
		{name: "values that don't order", text: `{{ sortBy (list (list 1)) "$" }}`, err: "error calling sortBy: can't sort by []interface {} values"},
		{name: "a value that isn't a list", text: `{{ sortBy "a" "$" }}`, err: "error calling sortBy: string is not a list"},
		{name: "mixed kinds", text: `{{ sortBy (list 1 "a") "$" }}`, err: "can't sort by both number and string values"},
		{name: "an invalid path", text: `{{ sortBy (list 1) "$[" }}`, err: "error calling sortBy: not terminated at 3 in $["},
	})
}
