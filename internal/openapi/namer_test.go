package openapi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestRouteFirst_NamesAMockByWhatItAnswers(t *testing.T) {
	// A folder listing groups a route's mocks together, and the name says which response each one is.
	tests := []struct {
		name string
		mock openapi.Mock
		want string
	}{
		{name: "a route", mock: openapi.Mock{Route: "/pets", Method: "GET", Status: "200", MediaType: "application/json"}, want: "pets.get.200.json"},
		{name: "a parameter", mock: openapi.Mock{Route: "/pets/{petId}", Method: "GET", Status: "200", MediaType: "application/xml"}, want: "pets._petId.get.200.xml"},
		{name: "no body", mock: openapi.Mock{Route: "/pets/{id}", Method: "DELETE", Status: "204"}, want: "pets._id.delete.204"},
		{name: "the root", mock: openapi.Mock{Route: "/", Method: "GET", Status: "200", MediaType: "text/plain"}, want: "root.get.200.text"},
		{name: "a range", mock: openapi.Mock{Route: "/a", Method: "POST", Status: "5XX"}, want: "a.post.5xx"},
		{name: "a vendor media type", mock: openapi.Mock{Route: "/a", Method: "GET", Status: "200", MediaType: "application/vnd.api+json; charset=utf-8"}, want: "a.get.200.vnd.api-json"},
		{name: "another media type", mock: openapi.Mock{Route: "/a", Method: "GET", Status: "200", MediaType: "image/png"}, want: "a.get.200.image-png"},
		{name: "a variant", mock: openapi.Mock{Route: "/a", Method: "GET", Status: "200", MediaType: "application/json", Variant: "sold out"}, want: "a.get.200.json.sold-out"},
		{name: "odd segments", mock: openapi.Mock{Route: "/users/{id}:activate/ä", Method: "POST", Status: "200"}, want: "users._id-activate.post.200"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openapi.RouteFirst(&tt.mock))
		})
	}
}

func TestGenerate_GivesEveryMockAUniqueIDEveryFileSystemTakes(t *testing.T) {
	// macOS and Windows ignore case in file names, Windows refuses its device names, and file names have a limit:
	// whatever the Namer says, every mock gets a file of its own.
	long := strings.Repeat("a", 250)
	names := []string{"Pets", "pets", "PETS", "con.get", long, long}

	ops := make([]openapi.Operation, len(names))
	for i := range names {
		ops[i] = openapi.Operation{Method: "GET", Path: "/" + string(rune('a'+i)), Responses: []openapi.Response{{Status: "200"}}}
	}

	out, err := openapi.Generate(&openapi.Document{Operations: ops}, openapi.WithNamer(func(m *openapi.Mock) string {
		return names[m.Route[1]-'a']
	}))
	require.NoError(t, err)

	got := make([]string, 0, len(out.Files))
	for _, f := range out.Files {
		got = append(got, f.Mocks[0].ID)
	}

	cut := strings.Repeat("a", 191) + "-15da3d4f"
	require.Equal(t, []string{"PETS", "Pets-2", "_con.get", cut[:198] + "-2", cut, "pets-3"}, got)
}
