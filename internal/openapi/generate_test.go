package openapi_test

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestGenerate_GivesEachMediaTypeAMockAndA406(t *testing.T) {
	// The client's Accept picks the representation: each mock must know every offer, so q-values decide, not the
	// mocks' order, and a client that takes none gets a 406, not a body it can't read.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
			{MediaType: "application/json", Examples: []openapi.Example{{Value: []any{"rex"}}}},
			{MediaType: "application/xml", Examples: []openapi.Example{{Value: "<pets/>"}}},
		}}},
	}}})

	offers := []any{"application/json", "application/xml"}

	require.Equal(t, []string{"pets.get.200.json", "pets.get.200.xml", "pets.get.406"}, ids(mocks))
	require.Equal(t, openapi.Mock{
		ID: "pets.get.200.json", Route: "/pets", Method: "GET", Status: "200", MediaType: "application/json",
		Name: "GET /pets", Priority: 300, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/pets"}, Accept: []any{"accept", "application/json", offers}},
		Reply:   openapi.Reply{Status: 200, Header: map[string]string{"Content-Type": "application/json"}, Body: []any{"rex"}},
	}, mocks["pets.get.200.json"])
	require.Equal(t, []any{"accept", "application/xml", offers}, mocks["pets.get.200.xml"].Request.Accept)
	require.Equal(t, "<pets/>", mocks["pets.get.200.xml"].Reply.Body)
	require.Equal(t, openapi.Mock{
		ID: "pets.get.406", Route: "/pets", Method: "GET", Status: "406",
		Name: "GET /pets", Priority: 400, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/pets"}, Accept: []any{"notacceptable", offers}},
		Reply: openapi.Reply{
			Status: 406, Header: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Body: "expected one of: application/json, application/xml",
		},
	}, mocks["pets.get.406"])
}

func TestGenerate_AMediaRangeGivesWayToMediaTypes(t *testing.T) {
	// A client asks for a media type: a range, such as */*, names none, so it can neither match an Accept nor be
	// sent as a Content-Type.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
			{MediaType: "*/*", Examples: []openapi.Example{{Value: "any"}}},
			{MediaType: "application/json", Examples: []openapi.Example{{Value: []any{}}}},
		}}},
	}}})

	require.Equal(t, []string{"pets.get.200.json", "pets.get.406"}, ids(mocks))
	require.Equal(t, []any{"accept", "application/json", []any{"application/json"}}, mocks["pets.get.200.json"].Request.Accept)
	require.Equal(t, []string{"GET /pets 200: media range */*: left out: the mocks answer application/json"}, warnings)
}

func TestGenerate_AResponseOfMediaRangesOnlyTakesAnyAccept(t *testing.T) {
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/files",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
			{MediaType: "text/*", Examples: []openapi.Example{{Value: "a file"}}},
			{MediaType: "*/*", Examples: []openapi.Example{{Value: "any"}}},
		}}},
	}}})

	require.Equal(t, []string{"files.get.200.text-any"}, ids(mocks))
	require.Equal(t, openapi.Mock{
		ID: "files.get.200.text-any", Route: "/files", Method: "GET", Status: "200", MediaType: "text/*",
		Name: "GET /files", Priority: 300, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/files"}},
		Reply:   openapi.Reply{Status: 200, Body: "a file"},
	}, mocks["files.get.200.text-any"])
	require.Equal(t, []string{"GET /files 200: media range */*: left out: the mocks answer text/*"}, warnings)
}

func TestGenerate_AnswersA406WithTheOperationsOwn(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets",
		Responses: []openapi.Response{
			{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Examples: []openapi.Example{{Value: []any{}}}}}},
			{Status: "406", Content: []openapi.Content{{MediaType: "application/problem+json", Examples: []openapi.Example{{Value: map[string]any{"title": "no"}}}}}},
		},
	}}})

	m := mocks["pets.get.406.problem-json"]
	require.Equal(t, []any{"notacceptable", []any{"application/json"}}, m.Request.Accept)
	require.Equal(t, openapi.Reply{
		Status: 406, Header: map[string]string{"Content-Type": "application/problem+json"}, Body: map[string]any{"title": "no"},
	}, m.Reply)
}

func TestGenerate_AResponseWithoutContentMatchesAnyAccept(t *testing.T) {
	// A 204 has no representation to negotiate: no Accept can make it a 406.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "DELETE", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id"}},
		Responses:  []openapi.Response{{Status: "204"}},
	}}})

	require.Equal(t, []string{"pets._id.delete.204"}, ids(mocks))
	require.Equal(t, openapi.Request{Method: "DELETE", PathTemplate: "/pets/{id}"}, mocks["pets._id.delete.204"].Request)
	require.Equal(t, openapi.Reply{Status: 204}, mocks["pets._id.delete.204"].Reply)
}

func TestGenerate_PathExamplesAndA404(t *testing.T) {
	// An example of a path parameter is a value the spec says exists: it gets the success, and with a 404, the
	// other values get the 404. Without examples, no value is known to be missing, so every value succeeds.
	ok := openapi.Response{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Examples: []openapi.Example{{Value: map[string]any{"id": 1}}}}}}
	notFound := openapi.Response{Status: "404"}
	example := []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Value: "a b"}}}}

	tests := []struct {
		name     string
		params   []openapi.Parameter
		res      []openapi.Response
		enabled  []string
		disabled []string
	}{
		{
			name: "an example and a 404", params: example, res: []openapi.Response{ok, notFound},
			enabled: []string{"pets._id.get.200.json.example", "pets._id.get.404", "pets._id.get.406"},
		},
		{
			name: "an example and no 404", params: example, res: []openapi.Response{ok},
			enabled: []string{"pets._id.get.200.json", "pets._id.get.200.json.example", "pets._id.get.406"},
		},
		{
			name: "a 404 and no example", params: []openapi.Parameter{{Name: "id"}}, res: []openapi.Response{ok, notFound},
			enabled: []string{"pets._id.get.200.json", "pets._id.get.406"}, disabled: []string{"pets._id.get.404"},
		},
		{
			name: "neither", params: []openapi.Parameter{{Name: "id"}}, res: []openapi.Response{ok},
			enabled: []string{"pets._id.get.200.json", "pets._id.get.406"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
				Method: "GET", Path: "/pets/{id}", PathParams: tt.params, Responses: tt.res,
			}}})

			require.Equal(t, tt.enabled, enabledIDs(mocks, true))
			require.Equal(t, tt.disabled, enabledIDs(mocks, false))
		})
	}

	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}", PathParams: example, Responses: []openapi.Response{ok, notFound},
	}}})

	require.Equal(t, openapi.Request{
		Method: "GET", Path: []any{"equal", "/pets/a b"}, Accept: []any{"accept", "application/json", []any{"application/json"}},
	}, mocks["pets._id.get.200.json.example"].Request)
	require.Equal(t, 1200, mocks["pets._id.get.200.json.example"].Priority)
	require.Equal(t, openapi.Request{Method: "GET", PathTemplate: "/pets/{id}"}, mocks["pets._id.get.404"].Request)
	require.Equal(t, 1500, mocks["pets._id.get.404"].Priority)
}

func TestGenerate_WritesAPathExampleInTheStyleOfItsParameter(t *testing.T) {
	// A client writes a path parameter in its style, as RFC 6570 does: the mock of an example matches that path, not
	// the bare value.
	list := []any{"blue", "black", "brown"}
	object := map[string]any{"R": float64(100), "G": float64(200), "B": float64(150)}

	tests := []struct {
		style   string
		explode bool
		value   any
		path    string
	}{
		{style: "simple", value: "blue", path: "/c/blue"},
		{style: "simple", value: list, path: "/c/blue,black,brown"},
		{style: "simple", explode: true, value: list, path: "/c/blue,black,brown"},
		{style: "simple", value: object, path: "/c/B,150,G,200,R,100"},
		{style: "simple", explode: true, value: object, path: "/c/B=150,G=200,R=100"},
		{style: "label", value: "", path: "/c/."},
		{style: "label", value: "blue", path: "/c/.blue"},
		{style: "label", value: list, path: "/c/.blue,black,brown"},
		{style: "label", explode: true, value: list, path: "/c/.blue.black.brown"},
		{style: "label", value: object, path: "/c/.B,150,G,200,R,100"},
		{style: "label", explode: true, value: object, path: "/c/.B=150.G=200.R=100"},
		{style: "matrix", value: "", path: "/c/;color"},
		{style: "matrix", value: float64(7), path: "/c/;color=7"},
		{style: "matrix", value: list, path: "/c/;color=blue,black,brown"},
		{style: "matrix", explode: true, value: list, path: "/c/;color=blue;color=black;color=brown"},
		{style: "matrix", value: object, path: "/c/;color=B,150,G,200,R,100"},
		{style: "matrix", explode: true, value: object, path: "/c/;B=150;G=200;R=100"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s explode=%v %v", tt.style, tt.explode, tt.value), func(t *testing.T) {
			mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
				Method: "GET", Path: "/c/{color}",
				PathParams: []openapi.Parameter{{Name: "color", Style: tt.style, Explode: tt.explode, Examples: []openapi.Example{{Value: tt.value}}}},
				Responses:  []openapi.Response{{Status: "200"}},
			}}})

			require.Equal(t, []any{"equal", tt.path}, mocks["c._color.get.200.example"].Request.Path)
		})
	}
}

func TestGenerate_NamedPathExamplesGetAMockEach(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Name: "dog", Value: 1}, {Name: "cat", Value: 2}}}},
		Responses:  []openapi.Response{{Status: "200"}},
	}}})

	require.Equal(t, []any{"equal", "/pets/1"}, mocks["pets._id.get.200.dog"].Request.Path)
	require.Equal(t, []any{"equal", "/pets/2"}, mocks["pets._id.get.200.cat"].Request.Path)
}

func TestGenerate_WritesWhatNothingPredictsDisabled(t *testing.T) {
	// A 503 or a second success is a response the spec allows, not one a request asks for: it waits for the admin
	// API to enable it. The statuses a request's contract decides get no mock: the contract answers them.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "POST", Path: "/pets",
		Responses: []openapi.Response{
			{Status: "201"},
			{Status: "202"},
			{Status: "400"},
			{Status: "401"},
			{Status: "403"},
			{Status: "405"},
			{Status: "415"},
			{Status: "422"},
			{Status: "5XX"},
			{Status: "default"},
		},
	}}})

	require.Equal(t, []string{"pets.post.201"}, enabledIDs(mocks, true))
	require.Equal(t, []string{"pets.post.202", "pets.post.403", "pets.post.5xx", "pets.post.default"}, enabledIDs(mocks, false))
	require.Equal(t, 500, mocks["pets.post.5xx"].Reply.Status)
	require.Equal(t, 500, mocks["pets.post.default"].Reply.Status)
	require.Equal(t, 100, mocks["pets.post.403"].Priority)
}

func TestGenerate_PicksTheSuccessResponse(t *testing.T) {
	// Swagger-era specs often declare only a default: it is then the answer to a request that succeeds.
	tests := []struct {
		name     string
		statuses []string
		want     string
		status   int
	}{
		{name: "the lowest 2xx", statuses: []string{"204", "200", "default"}, want: "200", status: 200},
		{name: "a range", statuses: []string{"2XX", "302"}, want: "2xx", status: 200},
		{name: "a redirect", statuses: []string{"302", "default"}, want: "302", status: 302},
		{name: "only a default", statuses: []string{"default"}, want: "default", status: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := make([]openapi.Response, len(tt.statuses))
			for i, s := range tt.statuses {
				res[i] = openapi.Response{Status: s}
			}

			mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{Method: "GET", Path: "/x", Responses: res}}})

			m := mocks["x.get."+tt.want]
			require.True(t, m.Enabled)
			require.Equal(t, 300, m.Priority)
			require.Equal(t, tt.status, m.Reply.Status)
			require.Equal(t, []string{"x.get." + tt.want}, enabledIDs(mocks, true))
		})
	}
}

func TestGenerate_ExamplesPastTheFirstAreDisabled(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{
			MediaType: "text/plain",
			Examples:  []openapi.Example{{Name: "one", Value: "a"}, {Name: "two", Value: "b"}, {Value: 3}},
		}}}},
	}}})

	require.Equal(t, "a", mocks["pets.get.200.text"].Reply.Body)
	require.True(t, mocks["pets.get.200.text"].Enabled)
	require.Equal(t, "b", mocks["pets.get.200.text.two"].Reply.Body)
	require.False(t, mocks["pets.get.200.text.two"].Enabled)
	require.Equal(t, 100, mocks["pets.get.200.text.two"].Priority)
	require.Equal(t, "3", mocks["pets.get.200.text.example3"].Reply.Body)
}

func TestGenerate_MoreTemplatedSegmentsMatchLater(t *testing.T) {
	// OpenAPI matches a concrete path before a templated one: /pets/mine must not answer as /pets/{id}, not even by
	// a mock of /pets/{id} in an earlier band, such as one enabled through the admin API.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/pets/mine", Responses: []openapi.Response{{Status: "200"}, {Status: "503"}}},
		{Method: "GET", Path: "/pets/{id}", PathParams: []openapi.Parameter{{Name: "id"}}, Responses: []openapi.Response{{Status: "200"}, {Status: "503"}}},
		{Method: "GET", Path: "/pets/{id}/{photo}", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Equal(t, 100, mocks["pets.mine.get.503"].Priority)
	require.Equal(t, 300, mocks["pets.mine.get.200"].Priority)
	require.Equal(t, 1100, mocks["pets._id.get.503"].Priority)
	require.Equal(t, 1300, mocks["pets._id.get.200"].Priority)
	require.Equal(t, 2300, mocks["pets._id._photo.get.200"].Priority)
}

func TestGenerate_MatchesAParameterInsideASegmentWithARegularExpression(t *testing.T) {
	// A path template takes whole segments only.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/files/{name}.{ext}", BasePath: "/v1", Responses: []openapi.Response{{Status: "200"}},
	}}})

	m := mocks["v1.files._name._ext.get.200"]
	require.Equal(t, []any{"regex", `^/v1/files/[^/]+\.[^/]+$`}, m.Request.Path)
	require.Equal(t, "/v1/files/{name}.{ext}", m.Route)
	require.Equal(t, 1300, m.Priority)
}

func TestGenerate_PutsEveryPathUnderTheBasePath(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/", BasePath: "/v1/", Responses: []openapi.Response{{Status: "200"}}},
		{Method: "GET", Path: "/pets", BasePath: "/v1/", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Equal(t, []any{"equal", "/v1"}, mocks["v1.get.200"].Request.Path)
	require.Equal(t, []any{"equal", "/v1/pets"}, mocks["v1.pets.get.200"].Request.Path)
}

func TestGenerate_ASchemaKeepsItsOwnDefs(t *testing.T) {
	// OpenAPI 3.1 schemas are JSON Schema: one may hold $defs of its own, next to the document's shared schemas.
	mocks := generate(t, &openapi.Document{
		Defs: map[string]any{"Pet": map[string]any{"const": "pet"}},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/a",
			Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{
				"$defs": map[string]any{"Name": map[string]any{"const": "rex"}},
				"type":  "array", "minItems": 2, "maxItems": 2,
				"prefixItems": []any{map[string]any{"$ref": "#/$defs/Name"}, map[string]any{"$ref": "#/$defs/Pet"}},
			}}}}},
		}},
	})

	require.Equal(t, []any{"rex", "pet"}, mocks["a.get.200.json"].Reply.Body)
}

func TestGenerate_LeavesOutWriteOnlyPropertiesOfASharedSchemaOfAnyName(t *testing.T) {
	// The shared schemas are named by the spec: one named properties is a schema like the others.
	mocks := generate(t, &openapi.Document{
		Defs: map[string]any{"properties": map[string]any{
			"type": "object", "required": []any{"a"},
			"properties": map[string]any{"a": map[string]any{"type": "string", "writeOnly": true}},
		}},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/a",
			Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{"$ref": "#/$defs/properties"}}}}},
		}},
	})

	require.Equal(t, make(map[string]any), mocks["a.get.200.json"].Reply.Body)
}

func TestGenerate_LeavesOutAPropertyThatRefersToAWriteOnlySchema(t *testing.T) {
	// A property's schema may be shared, through any number of references: it is writeOnly when the schema they end
	// at is. A cycle of references ends nowhere, and doesn't keep the generator from finishing.
	mocks := generate(t, &openapi.Document{
		Defs: map[string]any{
			"Password": map[string]any{"$ref": "#/$defs/Secret"},
			"Secret":   map[string]any{"type": "string", "const": "very-secret", "writeOnly": true},
			"Loop":     map[string]any{"$ref": "#/$defs/Again"},
			"Again":    map[string]any{"$ref": "#/$defs/Loop"},
		},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/a",
			Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{
				"type": "object", "required": []any{"password", "name"},
				"properties": map[string]any{
					"password": map[string]any{"$ref": "#/$defs/Password"},
					"name":     map[string]any{"const": "rex"},
					"loop":     map[string]any{"$ref": "#/$defs/Loop"},
				},
			}}}}},
		}},
	})

	require.Equal(t, map[string]any{"name": "rex"}, mocks["a.get.200.json"].Reply.Body)
}

func TestGenerate_FakesWhatHasNoExample(t *testing.T) {
	// Fake values fit the schema, refer to the shared ones, and leave out what a response never shows.
	doc := &openapi.Document{
		Defs: map[string]any{"Pet": map[string]any{
			"type":     "object",
			"required": []any{"name", "password"},
			"properties": map[string]any{
				"name":     map[string]any{"const": "Rex"},
				"password": map[string]any{"type": "string", "writeOnly": true},
			},
		}},
		Operations: []openapi.Operation{{
			Method: "GET", Path: "/pet",
			Responses: []openapi.Response{{
				Status:  "200",
				Headers: []openapi.Header{{Name: "X-Rate", Schema: map[string]any{"const": 5}}, {Name: "X-Tags", Examples: []openapi.Example{{Value: []any{"a", 1}}}}},
				Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{"$ref": "#/$defs/Pet"}}},
			}},
		}},
	}

	m := generate(t, doc)["pet.get.200.json"]
	require.Equal(t, openapi.Reply{
		Status: 200,
		Header: map[string]string{"Content-Type": "application/json", "X-Rate": "5", "X-Tags": "a,1"},
		Body:   map[string]any{"name": "Rex"},
	}, m.Reply)
}

func TestGenerate_WritesTheSameMocksOnEveryRun(t *testing.T) {
	// A regenerated folder diffs cleanly in git only when a rerun writes the same values, whatever else the spec
	// holds.
	op := openapi.Operation{Method: "GET", Path: "/pets", Responses: []openapi.Response{{
		Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{"type": "string", "format": "uuid"}}},
	}}}
	other := openapi.Operation{Method: "GET", Path: "/other", Responses: []openapi.Response{{
		Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{"type": "string"}}},
	}}}

	first := generate(t, &openapi.Document{Operations: []openapi.Operation{op}})["pets.get.200.json"]
	second := generate(t, &openapi.Document{Operations: []openapi.Operation{other, op}})["pets.get.200.json"]

	require.Equal(t, first, second)
	require.IsType(t, "", first.Reply.Body)
	require.Len(t, first.Reply.Body, 38) // a quoted UUID
}

func TestGenerate_DisablesAMockNoBodyFits(t *testing.T) {
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/n", Responses: []openapi.Response{{
			Status: "200", Content: []openapi.Content{{MediaType: "application/json", Schema: map[string]any{"type": "integer", "minimum": 5, "maximum": 1}}},
		}},
	}}})

	m := mocks["n.get.200.json"]
	require.False(t, m.Enabled)
	require.Nil(t, m.Reply.Body)
	require.Equal(t, []string{
		"GET /n 200 application/json: no body fits: fakefy: no value satisfies the schema: at the root: minimum 5 is above maximum 1: written disabled",
	}, warnings)
}

func TestGenerate_NumbersUnnamedPathExamples(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Value: 1}, {Value: 2}}}},
		Responses:  []openapi.Response{{Status: "200"}},
	}}})

	require.Equal(t, []any{"equal", "/pets/1"}, mocks["pets._id.get.200.example1"].Request.Path)
	require.Equal(t, []any{"equal", "/pets/2"}, mocks["pets._id.get.200.example2"].Request.Path)
}

func TestGenerate_LeavesOutAHeaderNoValueFits(t *testing.T) {
	// A header the spec gives no value, or one no value satisfies, is better missing than wrong.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/a",
		Responses: []openapi.Response{{Status: "204", Headers: []openapi.Header{
			{Name: "X-Free"},
			{Name: "X-None", Schema: map[string]any{"type": "integer", "minimum": 5, "maximum": 1}},
			{Name: "X-Ok", Schema: map[string]any{"const": "ok"}},
		}}},
	}}})

	require.Equal(t, map[string]string{"X-Ok": "ok"}, mocks["a.get.204"].Reply.Header)
	require.Equal(t, []string{
		"GET /a 204: no value of header X-None fits: fakefy: no value satisfies the schema: at the root: minimum 5 is above maximum 1: left out",
	}, warnings)
}

func TestGenerate_ABooleanSchemaAllowsAnythingOrNothing(t *testing.T) {
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/any", Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "text/plain", Schema: true}}}}},
		{Method: "GET", Path: "/none", Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "text/plain", Schema: false}}}}},
	}})

	require.True(t, mocks["any.get.200.text"].Enabled)
	require.NotNil(t, mocks["any.get.200.text"].Reply.Body)
	require.False(t, mocks["none.get.200.text"].Enabled)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "GET /none 200 text/plain: no body fits: ")
}

func TestGenerate_ARedirectRangeIsTheSuccessWithoutA2xx(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/old",
		Responses: []openapi.Response{{Status: "default"}, {Status: "3XX"}},
	}}})

	require.True(t, mocks["old.get.3xx"].Enabled)
	require.Equal(t, 300, mocks["old.get.3xx"].Reply.Status)
	require.False(t, mocks["old.get.default"].Enabled)
}

// generate returns the mocks Generate makes of doc, by ID, and fails on a warning.
func generate(t *testing.T, doc *openapi.Document) map[string]openapi.Mock {
	t.Helper()

	mocks, warnings := generateWithWarnings(t, doc)
	require.Empty(t, warnings)

	return mocks
}

func generateWithWarnings(t *testing.T, doc *openapi.Document) (map[string]openapi.Mock, []string) {
	t.Helper()

	out := openapi.Generate(doc)

	mocks := make(map[string]openapi.Mock, len(out.Files))
	for _, f := range out.Files {
		require.Len(t, f.Mocks, 1)
		require.Equal(t, f.Mocks[0].ID+".mock.yaml", f.Name)
		mocks[f.Mocks[0].ID] = f.Mocks[0]
	}

	return mocks, out.Warnings
}

func ids(mocks map[string]openapi.Mock) []string {
	return slices.Sorted(maps.Keys(mocks))
}

func enabledIDs(mocks map[string]openapi.Mock, enabled bool) []string {
	var out []string
	for _, id := range ids(mocks) {
		if mocks[id].Enabled == enabled {
			out = append(out, id)
		}
	}

	return out
}
