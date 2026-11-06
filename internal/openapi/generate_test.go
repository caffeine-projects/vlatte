package openapi_test

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/rs/zerolog"
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

	require.Equal(t, []string{"pets-get-200-json", "pets-get-200-xml", "pets-get-406"}, ids(mocks))
	require.Equal(t, openapi.Mock{
		ID: "pets-get-200-json", Route: "/pets", Method: "GET", Status: "200", MediaType: "application/json",
		Name: "GET /pets", Priority: 300, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/pets"}, Accept: []any{"accept", "application/json", offers}},
		Reply:   openapi.Reply{Status: 200, Header: map[string]string{"Content-Type": "application/json"}, Body: []any{"rex"}},
	}, mocks["pets-get-200-json"])
	require.Equal(t, []any{"accept", "application/xml", offers}, mocks["pets-get-200-xml"].Request.Accept)
	require.Equal(t, "<pets/>", mocks["pets-get-200-xml"].Reply.Body)
	require.Equal(t, openapi.Mock{
		ID: "pets-get-406", Route: "/pets", Method: "GET", Status: "406",
		Name: "GET /pets", Priority: 400, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/pets"}, Accept: []any{"notacceptable", offers}},
		Reply: openapi.Reply{
			Status: 406, Header: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Body: "expected one of: application/json, application/xml",
		},
	}, mocks["pets-get-406"])
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

	require.Equal(t, []string{"pets-get-200-json", "pets-get-406"}, ids(mocks))
	require.Equal(t, []any{"accept", "application/json", []any{"application/json"}}, mocks["pets-get-200-json"].Request.Accept)
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "pets-get-200-json", "method": "GET", "route": "/pets", "status": "200", "media_type": "application/json",
		"message": "media range */*: left out: the mocks answer application/json",
	}}, warnings)
}

func TestGenerate_AResponseOfMediaRangesOnlyTakesAnyAccept(t *testing.T) {
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/files",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{
			{MediaType: "text/*", Examples: []openapi.Example{{Value: "a file"}}},
			{MediaType: "*/*", Examples: []openapi.Example{{Value: "any"}}},
		}}},
	}}})

	require.Equal(t, []string{"files-get-200-text-any"}, ids(mocks))
	require.Equal(t, openapi.Mock{
		ID: "files-get-200-text-any", Route: "/files", Method: "GET", Status: "200", MediaType: "text/*",
		Name: "GET /files", Priority: 300, Enabled: true,
		Request: openapi.Request{Method: "GET", Path: []any{"equal", "/files"}},
		Reply:   openapi.Reply{Status: 200, Body: "a file"},
	}, mocks["files-get-200-text-any"])
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "files-get-200-text-any", "method": "GET", "route": "/files", "status": "200", "media_type": "text/*",
		"message": "media range */*: left out: the mocks answer text/*",
	}}, warnings)
}

func TestGenerate_AnswersA406WithTheOperationsOwn(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets",
		Responses: []openapi.Response{
			{Status: "200", Content: []openapi.Content{{MediaType: "application/json", Examples: []openapi.Example{{Value: []any{}}}}}},
			{Status: "406", Content: []openapi.Content{{MediaType: "application/problem+json", Examples: []openapi.Example{{Value: map[string]any{"title": "no"}}}}}},
		},
	}}})

	m := mocks["pets-get-406-problem-json"]
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

	require.Equal(t, []string{"pets-_id-delete-204"}, ids(mocks))
	require.Equal(t, openapi.Request{Method: "DELETE", PathTemplate: "/pets/{id}"}, mocks["pets-_id-delete-204"].Request)
	require.Equal(t, openapi.Reply{Status: 204}, mocks["pets-_id-delete-204"].Reply)
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
			enabled: []string{"pets-_id-get-200-json-example", "pets-_id-get-404", "pets-_id-get-406"},
		},
		{
			name: "an example and no 404", params: example, res: []openapi.Response{ok},
			enabled: []string{"pets-_id-get-200-json", "pets-_id-get-200-json-example", "pets-_id-get-406"},
		},
		{
			name: "a 404 and no example", params: []openapi.Parameter{{Name: "id"}}, res: []openapi.Response{ok, notFound},
			enabled: []string{"pets-_id-get-200-json", "pets-_id-get-406"}, disabled: []string{"pets-_id-get-404"},
		},
		{
			name: "neither", params: []openapi.Parameter{{Name: "id"}}, res: []openapi.Response{ok},
			enabled: []string{"pets-_id-get-200-json", "pets-_id-get-406"},
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
	}, mocks["pets-_id-get-200-json-example"].Request)
	require.Equal(t, 1200, mocks["pets-_id-get-200-json-example"].Priority)
	require.Equal(t, openapi.Request{Method: "GET", PathTemplate: "/pets/{id}"}, mocks["pets-_id-get-404"].Request)
	require.Equal(t, 1500, mocks["pets-_id-get-404"].Priority)
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

			require.Equal(t, []any{"equal", tt.path}, mocks["c-_color-get-200-example"].Request.Path)
		})
	}
}

func TestGenerate_WritesAPathExampleOfAParameterWithContentInItsMediaType(t *testing.T) {
	// A parameter with content is written in its media type, as a body is: JSON in JSON, text as is. Data can't be
	// text: that example is left out, with a warning.
	tests := []struct {
		mediaType string
		value     any
		path      string // "" for no example mock
	}{
		{mediaType: "application/json", value: "blue", path: `/c/"blue"`},
		{mediaType: "application/json", value: map[string]any{"R": float64(100), "B": float64(150)}, path: `/c/{"B":150,"R":100}`},
		{mediaType: "text/plain", value: "blue", path: "/c/blue"},
		{mediaType: "text/plain", value: []any{"blue"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %v", tt.mediaType, tt.value), func(t *testing.T) {
			mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
				Method: "GET", Path: "/c/{color}",
				PathParams: []openapi.Parameter{{Name: "color", Style: "simple", MediaType: tt.mediaType, Examples: []openapi.Example{{Value: tt.value}}}},
				Responses:  []openapi.Response{{Status: "200"}},
			}}})

			if tt.path == "" {
				require.NotContains(t, mocks, "c-_color-get-200-example")
				require.Equal(t, []map[string]any{{
					"level": "warn", "mock": "c-_color-get-200", "method": "GET", "route": "/c/{color}", "status": "200",
					"message": "path parameter color is data, not text: left out: give a string example",
				}}, warnings)

				return
			}

			require.Equal(t, []any{"equal", tt.path}, mocks["c-_color-get-200-example"].Request.Path)
			require.Empty(t, warnings)
		})
	}
}

func TestGenerate_WarnsOfAPathExampleOnlyForTheMocksOfItsOperation(t *testing.T) {
	// An operation without a success makes no example mock: an example it can't write is no news, and a warning
	// must not land on the mock of another operation.
	_, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{
		{
			Method: "GET", Path: "/a/{c}",
			PathParams: []openapi.Parameter{{Name: "c", Style: "simple", MediaType: "text/plain", Examples: []openapi.Example{{Value: []any{"blue"}}}}},
			Responses:  []openapi.Response{{Status: "400"}},
		},
		{Method: "GET", Path: "/b", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Empty(t, warnings)
}

func TestGenerate_EscapesInAPathTemplateAPercentThatStartsNoEscape(t *testing.T) {
	// A % that starts no escape is a percent sign, which a path template writes %25: the escapes beside it stay.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/rate/50%/{id}", Responses: []openapi.Response{{Status: "200"}}},
		{Method: "GET", Path: "/off%2010%/{id}", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Equal(t, "/rate/50%25/{id}", mocks["rate-50-_id-get-200"].Request.PathTemplate)
	require.Equal(t, "/off%2010%25/{id}", mocks["off-2010-_id-get-200"].Request.PathTemplate)
}

func TestGenerate_A404AnswersInTheMediaTypeTheClientPrefers(t *testing.T) {
	// A 404 is a response like the others: a client gets it in the media type its Accept prefers, and one whose
	// Accept takes none of them still learns the id is unknown, in the first.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Value: "1"}}}},
		Responses: []openapi.Response{
			{Status: "200", Content: []openapi.Content{
				{MediaType: "application/json", Examples: []openapi.Example{{Value: map[string]any{"id": "1"}}}},
				{MediaType: "application/xml", Examples: []openapi.Example{{Value: "<pet/>"}}},
			}},
			{Status: "404", Content: []openapi.Content{
				{MediaType: "*/*", Examples: []openapi.Example{{Value: "missing"}}},
				{MediaType: "application/json", Examples: []openapi.Example{{Value: map[string]any{"error": "missing"}}}},
				{MediaType: "application/xml", Examples: []openapi.Example{{Value: "<missing/>"}}},
			}},
		},
	}}})

	offers := []any{"application/json", "application/xml"}

	require.Equal(t, []string{
		"pets-_id-get-200-json-example", "pets-_id-get-200-xml-example", "pets-_id-get-404-json", "pets-_id-get-404-xml", "pets-_id-get-406",
	}, ids(mocks))
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "pets-_id-get-404-json", "method": "GET", "route": "/pets/{id}", "status": "404", "media_type": "application/json",
		"message": "media range */*: left out: the mocks answer application/json",
	}}, warnings)

	json := mocks["pets-_id-get-404-json"]
	require.Equal(t, []any{"any", []any{"accept", "application/json", offers}, []any{"notacceptable", offers}}, json.Request.Accept)
	require.Equal(t, map[string]any{"error": "missing"}, json.Reply.Body)
	require.Equal(t, 1500, json.Priority)

	xml := mocks["pets-_id-get-404-xml"]
	require.Equal(t, []any{"accept", "application/xml", offers}, xml.Request.Accept)
	require.Equal(t, "<missing/>", xml.Reply.Body)
	require.Equal(t, "/pets/{id}", xml.Request.PathTemplate)
	require.Equal(t, 1500, xml.Priority)
}

func TestGenerate_A404OfOneMediaTypeTakesAnyAccept(t *testing.T) {
	// With one representation, there is nothing for Accept to choose.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Value: "1"}}}},
		Responses: []openapi.Response{
			{Status: "200"},
			{Status: "404", Content: []openapi.Content{{MediaType: "application/json", Examples: []openapi.Example{{Value: "missing"}}}}},
		},
	}}})

	require.Nil(t, mocks["pets-_id-get-404-json"].Request.Accept)
}

func TestGenerate_FillsEachPathParameterOnceFromTheTemplate(t *testing.T) {
	// An example is a value: a { in it is a character the client writes, not a parameter to fill.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/p/{a}/{b}",
		PathParams: []openapi.Parameter{
			{Name: "a", Style: "simple", Examples: []openapi.Example{{Value: "{b}"}}},
			{Name: "b", Style: "simple", Examples: []openapi.Example{{Value: "1"}}},
		},
		Responses: []openapi.Response{{Status: "200"}},
	}, {
		// A placeholder the operation declares no parameter for stays as the spec writes it.
		Method: "GET", Path: "/q/{a}/{c}",
		PathParams: []openapi.Parameter{{Name: "a", Style: "simple", Examples: []openapi.Example{{Value: "1"}}}},
		Responses:  []openapi.Response{{Status: "200"}},
	}}})

	require.Equal(t, []any{"equal", "/p/{b}/1"}, mocks["p-_a-_b-get-200-example"].Request.Path)
	require.Equal(t, []any{"equal", "/q/1/{c}"}, mocks["q-_a-_c-get-200-example"].Request.Path)
}

func TestGenerate_NamedPathExamplesGetAMockEach(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Name: "dog", Value: 1}, {Name: "cat", Value: 2}}}},
		Responses:  []openapi.Response{{Status: "200"}},
	}}})

	require.Equal(t, []any{"equal", "/pets/1"}, mocks["pets-_id-get-200-dog"].Request.Path)
	require.Equal(t, []any{"equal", "/pets/2"}, mocks["pets-_id-get-200-cat"].Request.Path)
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

	require.Equal(t, []string{"pets-post-201"}, enabledIDs(mocks, true))
	require.Equal(t, []string{"pets-post-202", "pets-post-403", "pets-post-5xx", "pets-post-default"}, enabledIDs(mocks, false))
	require.Equal(t, 500, mocks["pets-post-5xx"].Reply.Status)
	require.Equal(t, 500, mocks["pets-post-default"].Reply.Status)
	require.Equal(t, 100, mocks["pets-post-403"].Priority)
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

			m := mocks["x-get-"+tt.want]
			require.True(t, m.Enabled)
			require.Equal(t, 300, m.Priority)
			require.Equal(t, tt.status, m.Reply.Status)
			require.Equal(t, []string{"x-get-" + tt.want}, enabledIDs(mocks, true))
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

	require.Equal(t, "a", mocks["pets-get-200-text"].Reply.Body)
	require.True(t, mocks["pets-get-200-text"].Enabled)
	require.Equal(t, "b", mocks["pets-get-200-text-two"].Reply.Body)
	require.False(t, mocks["pets-get-200-text-two"].Enabled)
	require.Equal(t, 100, mocks["pets-get-200-text-two"].Priority)
	require.Equal(t, "3", mocks["pets-get-200-text-example3"].Reply.Body)
}

func TestGenerate_WritesEachExamplePastTheFirstByItsOwnValue(t *testing.T) {
	// Enabled, an example's mock answers with its own body: one the generator can't write must not reach the client,
	// and one it can must name its media type, whatever the first example was.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/report",
		Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{
			MediaType: "text/csv",
			Examples: []openapi.Example{
				{Value: "a\n"}, {Name: "data", Value: map[string]any{"a": 1}}, {Name: "list", Value: []any{"a"}}, {Name: "csv", Value: "b\n"},
			},
		}}}},
	}}})

	csv := map[string]string{"Content-Type": "text/csv"}
	require.Equal(t, openapi.Reply{Status: 200, Header: csv, Body: "a\n"}, mocks["report-get-200-text-csv"].Reply)
	require.Equal(t, openapi.Reply{Status: 200}, mocks["report-get-200-text-csv-data"].Reply)
	require.Equal(t, openapi.Reply{Status: 200}, mocks["report-get-200-text-csv-list"].Reply)
	require.Equal(t, openapi.Reply{Status: 200, Header: csv, Body: "b\n"}, mocks["report-get-200-text-csv-csv"].Reply)

	message := "the body is data, not text: written disabled: give a string example"
	require.Equal(t, []map[string]any{
		{"level": "warn", "mock": "report-get-200-text-csv-data", "method": "GET", "route": "/report", "status": "200", "media_type": "text/csv", "variant": "data", "message": message},
		{"level": "warn", "mock": "report-get-200-text-csv-list", "method": "GET", "route": "/report", "status": "200", "media_type": "text/csv", "variant": "list", "message": message},
	}, warnings)
}

func TestGenerate_DisablesEveryMockWhoseBodyIsDataOutsideJSON(t *testing.T) {
	// A fake value of an object schema is data too, and a 404 or a 406 is written as the success is: none of them
	// may answer with JSON under a Content-Type that says otherwise.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Style: "simple", Examples: []openapi.Example{{Value: "1"}}}},
		Responses: []openapi.Response{
			{Status: "200", Content: []openapi.Content{{MediaType: "text/csv", Schema: map[string]any{"type": "object"}}}},
			{Status: "404", Content: []openapi.Content{{MediaType: "text/csv", Examples: []openapi.Example{{Value: map[string]any{"error": "missing"}}}}}},
			{Status: "406", Content: []openapi.Content{{MediaType: "application/xml", Examples: []openapi.Example{{Value: []any{"text/csv"}}}}}},
		},
	}}})

	require.Equal(t, []string{"pets-_id-get-200-text-csv-example", "pets-_id-get-404-text-csv", "pets-_id-get-406-xml"}, ids(mocks))

	for _, id := range ids(mocks) {
		require.False(t, mocks[id].Enabled, id)
		require.Nil(t, mocks[id].Reply.Body, id)
		require.Empty(t, mocks[id].Reply.Header, id)
	}

	message := "the body is data, not text: written disabled: give a string example"
	require.Equal(t, []map[string]any{
		{"level": "warn", "mock": "pets-_id-get-200-text-csv-example", "method": "GET", "route": "/pets/{id}", "status": "200", "media_type": "text/csv", "variant": "example", "message": message},
		{"level": "warn", "mock": "pets-_id-get-406-xml", "method": "GET", "route": "/pets/{id}", "status": "406", "media_type": "application/xml", "message": message},
		{"level": "warn", "mock": "pets-_id-get-404-text-csv", "method": "GET", "route": "/pets/{id}", "status": "404", "media_type": "text/csv", "message": message},
	}, warnings)
}

func TestGenerate_MoreTemplatedSegmentsMatchLater(t *testing.T) {
	// OpenAPI matches a concrete path before a templated one: /pets/mine must not answer as /pets/{id}, not even by
	// a mock of /pets/{id} in an earlier band, such as one enabled through the admin API.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/pets/mine", Responses: []openapi.Response{{Status: "200"}, {Status: "503"}}},
		{Method: "GET", Path: "/pets/{id}", PathParams: []openapi.Parameter{{Name: "id"}}, Responses: []openapi.Response{{Status: "200"}, {Status: "503"}}},
		{Method: "GET", Path: "/pets/{id}/{photo}", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Equal(t, 100, mocks["pets-mine-get-503"].Priority)
	require.Equal(t, 300, mocks["pets-mine-get-200"].Priority)
	require.Equal(t, 1100, mocks["pets-_id-get-503"].Priority)
	require.Equal(t, 1300, mocks["pets-_id-get-200"].Priority)
	require.Equal(t, 2300, mocks["pets-_id-_photo-get-200"].Priority)
}

func TestGenerate_MatchesAParameterInsideASegmentWithARegularExpression(t *testing.T) {
	// A path template takes whole segments only.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/files/{name}.{ext}", BasePath: "/v1", Responses: []openapi.Response{{Status: "200"}},
	}}})

	m := mocks["v1-files-_name-_ext-get-200"]
	require.Equal(t, []any{"regex", `^/v1/files/[^/]+\.[^/]+$`}, m.Request.Path)
	require.Equal(t, "/v1/files/{name}.{ext}", m.Route)
	require.Equal(t, 1300, m.Priority)
}

func TestGenerate_PutsEveryPathUnderTheBasePath(t *testing.T) {
	// A path of the spec is appended to the base path: / under /v1 is /v1/.
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/", BasePath: "/v1/", Responses: []openapi.Response{{Status: "200"}}},
		{Method: "GET", Path: "/pets", BasePath: "/v1/", Responses: []openapi.Response{{Status: "200"}}},
	}})

	require.Equal(t, []any{"equal", "/v1/"}, mocks["v1-get-200"].Request.Path)
	require.Equal(t, []any{"equal", "/v1/pets"}, mocks["v1-pets-get-200"].Request.Path)
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

	require.Equal(t, []any{"rex", "pet"}, mocks["a-get-200-json"].Reply.Body)
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

	require.Equal(t, make(map[string]any), mocks["a-get-200-json"].Reply.Body)
}

func TestGenerate_LeavesOutAPropertyThatRefersToAWriteOnlySchema(t *testing.T) {
	// A property's schema may be shared, through any number of references: it is writeOnly when the schema they end
	// at is, or one allOf applies to it. A cycle of references ends nowhere, and doesn't keep the generator from
	// finishing.
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
				"type": "object", "required": []any{"password", "token", "name", "nick"},
				"properties": map[string]any{
					"password": map[string]any{"$ref": "#/$defs/Password"},
					"token":    map[string]any{"allOf": []any{map[string]any{"minLength": 1}, map[string]any{"$ref": "#/$defs/Password"}}},
					"name":     map[string]any{"const": "rex"},
					"nick":     map[string]any{"allOf": []any{map[string]any{"type": "string"}, map[string]any{"const": "r"}}},
					"loop":     map[string]any{"$ref": "#/$defs/Loop"},
				},
			}}}}},
		}},
	})

	require.Equal(t, map[string]any{"name": "rex", "nick": "r"}, mocks["a-get-200-json"].Reply.Body)
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

	m := generate(t, doc)["pet-get-200-json"]
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

	first := generate(t, &openapi.Document{Operations: []openapi.Operation{op}})["pets-get-200-json"]
	second := generate(t, &openapi.Document{Operations: []openapi.Operation{other, op}})["pets-get-200-json"]

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

	m := mocks["n-get-200-json"]
	require.False(t, m.Enabled)
	require.Nil(t, m.Reply.Body)
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "n-get-200-json", "method": "GET", "route": "/n", "status": "200", "media_type": "application/json",
		"message": "no body fits: fakefy: no value satisfies the schema: at the root: minimum 5 is above maximum 1: written disabled",
	}}, warnings)
}

func TestGenerate_NumbersUnnamedPathExamples(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/pets/{id}",
		PathParams: []openapi.Parameter{{Name: "id", Examples: []openapi.Example{{Value: 1}, {Value: 2}}}},
		Responses:  []openapi.Response{{Status: "200"}},
	}}})

	require.Equal(t, []any{"equal", "/pets/1"}, mocks["pets-_id-get-200-example1"].Request.Path)
	require.Equal(t, []any{"equal", "/pets/2"}, mocks["pets-_id-get-200-example2"].Request.Path)
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

	require.Equal(t, map[string]string{"X-Ok": "ok"}, mocks["a-get-204"].Reply.Header)
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "a-get-204", "method": "GET", "route": "/a", "status": "204",
		"message": "no value of header X-None fits: fakefy: no value satisfies the schema: at the root: minimum 5 is above maximum 1: left out",
	}}, warnings)
}

func TestGenerate_WritesAHeaderWithContentInItsMediaType(t *testing.T) {
	// A header with content is its value in that media type: JSON text in JSON, the text itself in any other, where
	// data has no format the generator could pick.
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/a",
		Responses: []openapi.Response{{Status: "204", Headers: []openapi.Header{
			{Name: "X-Json", MediaType: "application/json", Examples: []openapi.Example{{Value: "ok"}}},
			{Name: "X-Fake", MediaType: "application/json", Schema: map[string]any{"const": []any{1}}},
			{Name: "X-Text", MediaType: "text/plain", Examples: []openapi.Example{{Value: 5}}},
			{Name: "X-Data", MediaType: "text/plain", Examples: []openapi.Example{{Value: map[string]any{"a": 1}}}},
		}}},
	}}})

	require.Equal(t, map[string]string{"X-Json": `"ok"`, "X-Fake": "[1]", "X-Text": "5"}, mocks["a-get-204"].Reply.Header)
	require.Equal(t, []map[string]any{{
		"level": "warn", "mock": "a-get-204", "method": "GET", "route": "/a", "status": "204",
		"message": "header X-Data is data, not text: left out: give a string example",
	}}, warnings)
}

func TestGenerate_ABooleanSchemaAllowsAnythingOrNothing(t *testing.T) {
	mocks, warnings := generateWithWarnings(t, &openapi.Document{Operations: []openapi.Operation{
		{Method: "GET", Path: "/any", Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "text/plain", Schema: true}}}}},
		{Method: "GET", Path: "/none", Responses: []openapi.Response{{Status: "200", Content: []openapi.Content{{MediaType: "text/plain", Schema: false}}}}},
	}})

	require.True(t, mocks["any-get-200-text"].Enabled)
	require.NotNil(t, mocks["any-get-200-text"].Reply.Body)
	require.False(t, mocks["none-get-200-text"].Enabled)
	require.Len(t, warnings, 1)
	require.Equal(t, "none-get-200-text", warnings[0]["mock"])
	require.Contains(t, warnings[0]["message"], "no body fits: ")
}

func TestGenerate_ARedirectRangeIsTheSuccessWithoutA2xx(t *testing.T) {
	mocks := generate(t, &openapi.Document{Operations: []openapi.Operation{{
		Method: "GET", Path: "/old",
		Responses: []openapi.Response{{Status: "default"}, {Status: "3XX"}},
	}}})

	require.True(t, mocks["old-get-3xx"].Enabled)
	require.Equal(t, 300, mocks["old-get-3xx"].Reply.Status)
	require.False(t, mocks["old-get-default"].Enabled)
}

// generate returns the mocks Generate makes of doc, by ID, and fails on a warning.
func generate(t *testing.T, doc *openapi.Document) map[string]openapi.Mock {
	t.Helper()

	mocks, warnings := generateWithWarnings(t, doc)
	require.Empty(t, warnings)

	return mocks
}

// generateWithWarnings returns the mocks Generate makes of doc, by ID, and the entries it logs.
func generateWithWarnings(t *testing.T, doc *openapi.Document) (map[string]openapi.Mock, []map[string]any) {
	t.Helper()

	var logs bytes.Buffer

	log := zerolog.New(&logs)
	files := openapi.Generate(doc, &log)

	mocks := make(map[string]openapi.Mock, len(files))
	for _, f := range files {
		require.Len(t, f.Mocks, 1)
		require.Equal(t, f.Mocks[0].ID+".mock.yaml", f.Name)
		mocks[f.Mocks[0].ID] = f.Mocks[0]
	}

	var entries []map[string]any

	for line := range bytes.Lines(logs.Bytes()) {
		var e map[string]any
		require.NoError(t, json.Unmarshal(line, &e))
		entries = append(entries, e)
	}

	return mocks, entries
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
