package e2e

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestOpenAPI_TheGeneratedMocksServeTheSpec(t *testing.T) {
	// What the generator writes is only worth what the server makes of it: the files must load, and answer each
	// request as the spec says, the example id with the pet, another id with a 404, a client that takes no offered
	// media type with a 406, and a concrete path before the templated one.
	dir := t.TempDir()

	loc, err := openapi.Location("testdata/openapi/pets.openapi.yaml")
	require.NoError(t, err)

	read := func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }

	data, err := read(loc)
	require.NoError(t, err)

	doc, err := openapiv3.Load(t.Context(), data, loc, read)
	require.NoError(t, err)

	out := openapi.Generate(doc)
	require.Empty(t, out.Warnings)

	_, err = openapi.Write(dir, doc.Title, out.Files)
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()).MockFilePatterns(filepath.Join(dir, "*.mock.yaml")))
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		name        string
		path        string
		accept      string
		status      int
		mock        string
		contentType string
	}{
		{name: "the example id", path: "/v1/pets/11111111-1111-4111-8111-111111111111", status: 200, mock: "v1.pets._id.get.200.json.example", contentType: "application/json"},
		{name: "the example id in XML", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "application/xml", status: 200, mock: "v1.pets._id.get.200.xml.example", contentType: "application/xml"},
		{name: "another id", path: "/v1/pets/33333333-3333-4333-8333-333333333333", status: 404, mock: "v1.pets._id.get.404.json", contentType: "application/json"},
		{name: "a media type not offered", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "text/csv", status: 406, mock: "v1.pets._id.get.406", contentType: "text/plain; charset=utf-8"},
		{name: "a concrete path", path: "/v1/pets/mine", status: 200, mock: "v1.pets.mine.get.200.json", contentType: "application/json"},
		{name: "an example that is escaped in a path", path: "/v1/tags/red%20fox", status: 200, mock: "v1.tags._tag.get.200.text.example", contentType: "text/plain"},
		{name: "another value of an escaped example", path: "/v1/tags/red", status: 404, mock: "v1.tags._tag.get.404"},
		{name: "a parameter inside a segment", path: "/v1/files/a.txt", status: 200, mock: "v1.files._name._ext.get.200.text", contentType: "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+tt.path, nil)
			require.NoError(t, err)

			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			require.Equal(t, tt.status, res.StatusCode, string(body))
			require.Equal(t, tt.mock, res.Header.Get(vlatte.HeaderMatchedMockID))
			require.Equal(t, tt.contentType, res.Header.Get("Content-Type"))
		})
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+"/v1/pets/mine", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	var pet map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &pet))
	require.NoError(t, res.Body.Close())
	require.Equal(t, map[string]any{"id": "22222222-2222-4222-8222-222222222222", "name": "Mine"}, pet)

	// A disabled mock is there to be enabled: once it is, it answers before the mocks the spec predicts.
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPatch, m.AdminURL()+"/mocks/v1.pets._id.get.503", strings.NewReader(`{"enabled": true}`))
	require.NoError(t, err)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL() + "/v1/pets/11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, "v1.pets._id.get.503", res.Header.Get(vlatte.HeaderMatchedMockID))
}

func TestOpenAPI_ALiteralPathAnswersBeforeAnOverlappingTemplate(t *testing.T) {
	// OpenAPI matches a literal path before a template that covers it: every mock of /pets/mine, its 406 too, must
	// answer before those of /pets/{id}, whatever their band.
	spec := `
openapi: 3.1.0
info: {title: Priority overlap, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}, example: mine}
      responses:
        "200":
          description: OK
          content:
            application/json: {example: {route: parameter}}
            application/xml: {example: <route>parameter</route>}
  /pets/mine:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json: {example: {route: literal}}
`
	m := serveSpec(t, spec)

	status, mock, body := call(t, m, http.MethodGet, "/pets/mine", "")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "pets.mine.get.200.json", mock)
	require.JSONEq(t, `{"route":"literal"}`, body)

	status, mock, body = call(t, m, http.MethodGet, "/pets/mine", "application/xml")
	require.Equal(t, http.StatusNotAcceptable, status, body)
	require.Equal(t, "pets.mine.get.406", mock)
}

func TestOpenAPI_PathAndOperationServersOverrideTheRootServers(t *testing.T) {
	// An operation's servers replace its path's, which replace the root's: each operation answers under its own.
	spec := `
openapi: 3.1.0
info: {title: Server overrides, version: "1"}
servers: [{url: /v1}]
paths:
  /pets:
    servers: [{url: /v2}]
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: path-level}
    post:
      servers: [{url: /v3}]
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: operation-level}
  /owners:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: root-level}
`
	m := serveSpec(t, spec)

	tests := []struct {
		method string
		path   string
		status int
		body   string
	}{
		{method: http.MethodGet, path: "/v2/pets", status: http.StatusOK, body: "path-level"},
		{method: http.MethodPost, path: "/v3/pets", status: http.StatusOK, body: "operation-level"},
		{method: http.MethodGet, path: "/v1/owners", status: http.StatusOK, body: "root-level"},
		{method: http.MethodGet, path: "/v1/pets", status: http.StatusTeapot},
		{method: http.MethodPost, path: "/v2/pets", status: http.StatusTeapot},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			status, _, body := call(t, m, tt.method, tt.path, "")
			require.Equal(t, tt.status, status, body)

			if tt.body != "" {
				require.Equal(t, tt.body, body)
			}
		})
	}
}

func TestOpenAPI_AStaticPathMatchesLiterally(t *testing.T) {
	// A * in a path the spec writes is a character of it, not a wildcard that takes the requests of another operation.
	spec := `
openapi: 3.1.0
info: {title: Literal star, version: "1"}
paths:
  /files/star*:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: literal-star}
  /files/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: parameter-route}
`
	m := serveSpec(t, spec)

	for path, want := range map[string]string{
		"/files/star*":     "literal-star",
		"/files/starlight": "parameter-route",
		"/files/another":   "parameter-route",
	} {
		t.Run(path, func(t *testing.T) {
			status, _, body := call(t, m, http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, status, body)
			require.Equal(t, want, body)
		})
	}
}

func TestOpenAPI_AReferencedWriteOnlyPropertyIsLeftOutOfAResponse(t *testing.T) {
	// A writeOnly property is never in a response, whether its schema is inline or shared.
	spec := `
openapi: 3.1.0
info: {title: Write only reference, version: "1"}
paths:
  /secret:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [password, name]
                properties:
                  password: {$ref: '#/components/schemas/Password'}
                  name: {type: string, const: rex}
components:
  schemas:
    Password: {$ref: '#/components/schemas/Secret'}
    Secret:
      type: string
      writeOnly: true
      const: very-secret
`
	m := serveSpec(t, spec)

	status, _, body := call(t, m, http.MethodGet, "/secret", "")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, `{"name":"rex"}`, body)
}

func TestOpenAPI_APathExampleIsSerializedInTheStyleOfItsParameter(t *testing.T) {
	// A client sends a matrix parameter as ;id=7: that is the request the example answers, and 7 alone is another
	// value, which the 404 answers.
	spec := `
openapi: 3.1.0
info: {title: Path serialization, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, style: matrix, schema: {type: integer}, example: 7}
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: found}
        "404":
          description: Not found
`
	m := serveSpec(t, spec)

	status, _, body := call(t, m, http.MethodGet, "/pets/;id=7", "text/plain")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "found", body)

	status, mock, body := call(t, m, http.MethodGet, "/pets/7", "text/plain")
	require.Equal(t, http.StatusNotFound, status, body)
	require.Equal(t, "pets._id.get.404", mock)
}

// serveSpec serves the mocks the generator writes of spec, an OpenAPI document.
func serveSpec(t *testing.T, spec string) *mockhttp.Server {
	t.Helper()

	specDir, mocksDir := t.TempDir(), t.TempDir()
	file := filepath.Join(specDir, "spec.yaml")
	require.NoError(t, os.WriteFile(file, []byte(spec), 0o600))

	loc, err := openapi.Location(file)
	require.NoError(t, err)

	read := func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }

	data, err := read(loc)
	require.NoError(t, err)

	doc, err := openapiv3.Load(t.Context(), data, loc, read)
	require.NoError(t, err)

	out := openapi.Generate(doc)
	require.Empty(t, out.Warnings)

	_, err = openapi.Write(mocksDir, doc.Title, out.Files)
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(mocksDir, "*.mock.yaml")))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// call sends a request to m, with accept as its Accept when not empty, and returns the status, the ID of the mock
// that answered and the body.
func call(t *testing.T, m *mockhttp.Server, method, path, accept string) (int, string, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, m.URL()+path, nil)
	require.NoError(t, err)

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode, res.Header.Get(vlatte.HeaderMatchedMockID), string(body)
}
