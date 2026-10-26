package mockhttp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathTemplate_Parse(t *testing.T) {
	// A parameter is a whole segment with a name, named once: anything else is refused when the mock loads, so a
	// template never silently matches nothing.
	for _, s := range []string{"/", "/users/{id}", "/users/{id}/orders/{orderId}", "/caf%C3%A9/{id}", "/a%7Bb%7D/{id}"} {
		_, err := parsePathTemplate(s)
		require.NoError(t, err, s)
	}

	tests := []struct {
		give    string
		wantErr string
	}{
		{"users/{id}", "a path template starts with /"},
		{"/users/{id}?page=2", "a path template is a path, without a query or a fragment"},
		{"/users/{id}#top", "a path template is a path, without a query or a fragment"},
		{"/users/a{id}", `segment "a{id}": a parameter is a whole segment, such as {id}`},
		{"/users/{id}x", `segment "{id}x": a parameter is a whole segment, such as {id}`},
		{"/users/{a{b}}", `segment "{a{b}}": a parameter is a whole segment, such as {id}`},
		{"/users/{", `segment "{": a parameter is a whole segment, such as {id}`},
		{"/users/{}", `segment "{}": a parameter has a name, such as {id}`},
		{"/users/{id}/orders/{id}", `parameter "id" is named twice`},
		{"/a%zz/{id}", `segment "a%zz": invalid URL escape "%zz"`},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			_, err := parsePathTemplate(tt.give)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestPathTemplate_Params(t *testing.T) {
	// A path fits a template when it has as many segments, the same literal ones once decoded, and a non-empty one
	// for each parameter, which comes decoded: as a relative url, /users/7/ isn't /users/7.
	tmpl, err := parsePathTemplate("/users/{id}/orders/{orderId}")
	require.NoError(t, err)

	tests := []struct {
		name string
		path string // as the client sent it, escaped
		want map[string]string
	}{
		{"the template", "/users/7/orders/BR-1", map[string]string{"id": "7", "orderId": "BR-1"}},
		{"an escaped slash stays in its parameter", "/users/a%2Fb/orders/9", map[string]string{"id": "a/b", "orderId": "9"}},
		{"a literal segment escaped", "/%75sers/7/orders/9", map[string]string{"id": "7", "orderId": "9"}},
		{"a segment fewer", "/users/7/orders", nil},
		{"a segment more", "/users/7/orders/9/items", nil},
		{"a trailing slash", "/users/7/orders/9/", nil},
		{"an empty parameter", "/users//orders/9", nil},
		{"a literal segment in another case", "/Users/7/orders/9", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, ok := tmpl.params(tt.path)
			require.Equal(t, tt.want != nil, ok)
			require.Equal(t, tt.want, params)
		})
	}

	root, err := parsePathTemplate("/")
	require.NoError(t, err)

	_, ok := root.params("/")
	require.True(t, ok, "/ fits /")

	_, ok = root.params("")
	require.False(t, ok, "a CONNECT's empty path fits nothing")
}
