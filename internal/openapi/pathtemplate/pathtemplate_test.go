package pathtemplate_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi/pathtemplate"
)

func TestTemplated_CountsTheSegmentsThatHoldAParameter(t *testing.T) {
	// A segment of two parameters is one templated segment: /files/{name}.{ext} is as specific as /files/{id}.
	tests := []struct {
		path string
		want int
	}{
		{path: "/pets/mine", want: 0},
		{path: "/pets/{id}", want: 1},
		{path: "/files/{name}.{ext}", want: 1},
		{path: "/owners/{owner}/pets/{id}", want: 2},
		{path: "/v1/{tenant}/pets/{id}/", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			require.Equal(t, tt.want, pathtemplate.Templated(tt.path))
		})
	}
}

func TestNewRoute_MatchesTheRequestsItsMocksAnswer(t *testing.T) {
	// The matcher checks the requests a route matches, and the generator writes the route into the mocks: both must
	// take the same requests. A path without parameters compares decoded, a template of whole segments splits the path
	// at each / first, as a mock's path_template does, and a parameter inside a segment matches the path decoded.
	tests := []struct {
		name   string
		base   string
		path   string
		target string
		kind   pathtemplate.Kind
		route  string
		params map[string]string
		ok     bool
	}{
		{name: "a literal path", path: "/pets/mine", target: "/pets/mine", kind: pathtemplate.Literal, route: "/pets/mine", ok: true},
		{name: "a literal path sent with an encoded slash", path: "/pets/mine", target: "/pets%2Fmine", kind: pathtemplate.Literal, route: "/pets/mine", ok: true},
		{name: "a literal path with an escape", path: "/a%20b/50%", target: "/a%20b/50%25", kind: pathtemplate.Literal, route: "/a b/50%", ok: true},
		{name: "a literal path under a base decoded once", base: "/v%20", path: "/input", target: "/v%2520/input", kind: pathtemplate.Literal, route: "/v%20/input", ok: true},
		{name: "another literal path", path: "/pets/mine", target: "/pets/mine/", kind: pathtemplate.Literal, route: "/pets/mine"},
		{
			name: "a template", base: "/v1/", path: "/pets/{id}", target: "/v1/pets/a%2Fb", kind: pathtemplate.Segments, route: "/v1/pets/{id}",
			params: map[string]string{"id": "a/b"}, ok: true,
		},
		{
			name: "a template under a base decoded once", base: "/v%20", path: "/pets/{id}", target: "/v%2520/pets/1", kind: pathtemplate.Segments,
			route: "/v%2520/pets/{id}", params: map[string]string{"id": "1"}, ok: true,
		},
		{name: "a template given a slash", path: "/pets/{id}", target: "/pets/a/b", kind: pathtemplate.Segments, route: "/pets/{id}"},
		{name: "a template given an empty segment", path: "/pets/{id}", target: "/pets/", kind: pathtemplate.Segments, route: "/pets/{id}"},
		{
			name: "a parameter inside a segment", base: "/v.1", path: "/files/{name}.{ext}", target: "/v.1/files/a%20b.txt", kind: pathtemplate.Pattern,
			route: `^/v\.1/files/[^/]+\.[^/]+$`, params: map[string]string{"name": "a b", "ext": "txt"}, ok: true,
		},
		{
			name: "a parameter inside a segment, sent an encoded slash", path: "/{dir}/{name}.txt", target: "/a%2Fb.txt", kind: pathtemplate.Pattern,
			route: `^/[^/]+/[^/]+\.txt$`, params: map[string]string{"dir": "a", "name": "b"}, ok: true,
		},
		{name: "a parameter inside a segment given a slash", path: "/files/{name}.txt", target: "/files/a%2Fb.txt", kind: pathtemplate.Pattern, route: `^/files/[^/]+\.txt$`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := pathtemplate.NewRoute(tt.base, tt.path)
			require.Equal(t, tt.kind, rt.Kind)
			require.Equal(t, tt.route, rt.Path)

			u, err := url.Parse(tt.target)
			require.NoError(t, err)

			params, ok := rt.Match(u)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.params, params)
		})
	}
}

func TestNewExample_MatchesOnlyTheRequestsOfItsRoute(t *testing.T) {
	// The mock of an example answers before its route's, and must answer no request the route doesn't take, or that
	// request skips the checks of the operation. A whole segment's value is sent escaped, / included, a value inside a
	// segment is compared decoded, and an empty value matches nothing a route takes.
	tests := []struct {
		name   string
		base   string
		path   string
		values map[string]string
		kind   pathtemplate.Kind
		route  string
		ok     bool
	}{
		{name: "a value", base: "/v 1", path: "/pets/{id}", values: map[string]string{"{id}": "a b"}, kind: pathtemplate.Segments, route: "/v%201/pets/a%20b", ok: true},
		{name: "a value with a /", path: "/pets/{id}", values: map[string]string{"{id}": "a/b%"}, kind: pathtemplate.Segments, route: "/pets/a%2Fb%25", ok: true},
		{name: "a value like a parameter", path: "/pets/{id}", values: map[string]string{"{id}": "{x}"}, kind: pathtemplate.Segments, route: "/pets/%7Bx%7D", ok: true},
		{name: "a parameter without a value", path: "/pets/{id}/{x}", values: map[string]string{"{id}": "1"}, kind: pathtemplate.Segments, route: "/pets/1/{x}", ok: true},
		{name: "an empty value", path: "/pets/{id}", values: map[string]string{"{id}": ""}, kind: pathtemplate.Segments, route: "/pets/"},
		{name: "a value inside a segment", base: "/v1", path: "/pets/{id}.json", values: map[string]string{"{id}": "a b"}, kind: pathtemplate.Literal, route: "/v1/pets/a b.json", ok: true},
		{name: "a value with a / inside a segment", path: "/pets/{id}.json", values: map[string]string{"{id}": "a/b"}, kind: pathtemplate.Literal, route: "/pets/a/b.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex, ok := pathtemplate.NewExample(tt.base, tt.path, tt.values)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.kind, ex.Kind)
			require.Equal(t, tt.route, ex.Path)
		})
	}

	// The example of a whole segment takes its value sent escaped, and no request that sends a / as a /.
	ex, ok := pathtemplate.NewExample("", "/pets/{id}", map[string]string{"{id}": "a/b"})
	require.True(t, ok)

	for target, want := range map[string]bool{"/pets/a%2Fb": true, "/pets/a%2fb": true, "/pets/a/b": false, "/pets%2Fa%2Fb": false} {
		u, err := url.Parse(target)
		require.NoError(t, err)

		_, got := ex.Match(u)
		require.Equal(t, want, got, target)
	}
}
