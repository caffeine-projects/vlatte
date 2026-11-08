package e2e

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/match/matchopenapi"
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

	var logs bytes.Buffer

	log := zerolog.New(&logs)
	doc, err := openapiv3.Load(t.Context(), data, loc, read, &log)
	require.NoError(t, err)

	files, err := openapi.Generate(t.Context(), doc, &log)
	require.NoError(t, err)

	_, err = openapi.Write(dir, doc.Title, files)
	require.NoError(t, err)
	require.Empty(t, logs.String())

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()).MockFilePatterns(filepath.Join(dir, "*.mock.yaml")).
		Matchers(matchbuild.MustNew(matchopenapi.WithMatcher())))
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
		{name: "the example id", path: "/v1/pets/11111111-1111-4111-8111-111111111111", status: 200, mock: "v1-pets-_id-get-200-json-example", contentType: "application/json"},
		{name: "the example id in XML", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "application/xml", status: 200, mock: "v1-pets-_id-get-200-xml-example", contentType: "application/xml"},
		{name: "another id", path: "/v1/pets/33333333-3333-4333-8333-333333333333", status: 404, mock: "v1-pets-_id-get-404-json", contentType: "application/json"},
		{name: "a media type not offered", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "text/csv", status: 406, mock: "v1-pets-_id-get-406", contentType: "text/plain; charset=utf-8"},
		{name: "a concrete path", path: "/v1/pets/mine", status: 200, mock: "v1-pets-mine-get-200-json", contentType: "application/json"},
		{name: "an example that is escaped in a path", path: "/v1/tags/red%20fox", status: 200, mock: "v1-tags-_tag-get-200-text-example", contentType: "text/plain"},
		{name: "another value of an escaped example", path: "/v1/tags/red", status: 404, mock: "v1-tags-_tag-get-404"},
		{name: "a parameter inside a segment", path: "/v1/files/a.txt", status: 200, mock: "v1-files-_name-_ext-get-200-text", contentType: "text/plain"},
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
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPatch, m.AdminURL()+"/mocks/v1-pets-_id-get-503", strings.NewReader(`{"enabled": true}`))
	require.NoError(t, err)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL() + "/v1/pets/11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, "v1-pets-_id-get-503", res.Header.Get(vlatte.HeaderMatchedMockID))
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
	require.Equal(t, "pets-mine-get-200-json", mock)
	require.JSONEq(t, `{"route":"literal"}`, body)

	status, mock, body = call(t, m, http.MethodGet, "/pets/mine", "application/xml")
	require.Equal(t, http.StatusNotAcceptable, status, body)
	require.Equal(t, "pets-mine-get-406", mock)
}

func TestOpenAPI_PathAndOperationServersOverrideTheRootServers(t *testing.T) {
	// An operation's servers replace its path's, which replace the root's: each operation answers under its own, and
	// a path refuses there a method it has only under another.
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
		{method: http.MethodPost, path: "/v2/pets", status: http.StatusMethodNotAllowed},
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
	// A writeOnly property is never in a response, whether its schema is inline, shared, or a schema allOf
	// applies to it.
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
                required: [password, token, name]
                properties:
                  password: {$ref: '#/components/schemas/Password'}
                  token:
                    allOf: [{$ref: '#/components/schemas/Secret'}]
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

func TestOpenAPI_AWriteOnlyPropertyIsLeftOutWhicheverAllOfBranchRequiresIt(t *testing.T) {
	// The branches of allOf describe one object: a password one of them makes writeOnly is never in a response, even
	// when another branch, inline or shared, requires it.
	for _, version := range []string{"3.0.3", "3.1.0"} {
		t.Run(version, func(t *testing.T) {
			spec := `
openapi: ` + version + `
info: {title: Composed response, version: "1"}
paths:
  /inline:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                allOf:
                  - type: object
                    properties:
                      password: {type: string, writeOnly: true}
                  - type: object
                    required: [password, name]
                    properties:
                      name: {type: string, enum: [rex]}
  /shared:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                allOf:
                  - $ref: '#/components/schemas/Account'
                  - properties:
                      password: {type: string, writeOnly: true}
components:
  schemas:
    Account:
      type: object
      required: [password, name]
      properties:
        password: {type: string}
        name: {type: string, enum: [rex]}
`
			m := serveSpec(t, spec)

			for _, path := range []string{"/inline", "/shared"} {
				status, _, body := call(t, m, http.MethodGet, path, "")
				require.Equal(t, http.StatusOK, status, body)
				require.JSONEq(t, `{"name":"rex"}`, body, path)
			}
		})
	}
}

func TestOpenAPI_APathExampleIsSerializedInTheStyleOfItsParameter(t *testing.T) {
	// A client sends a matrix parameter as ;id=7: that is the request the example answers, ;id=8 is another value,
	// which the 404 answers, and 7 alone breaks the spec.
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

	status, mock, body := call(t, m, http.MethodGet, "/pets/;id=8", "text/plain")
	require.Equal(t, http.StatusNotFound, status, body)
	require.Equal(t, "pets-_id-get-404", mock)

	status, mock, body = call(t, m, http.MethodGet, "/pets/7", "text/plain")
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Equal(t, "pets-_id-get-400", mock)
}

func TestOpenAPI_A404AnswersInTheMediaTypeTheClientPrefers(t *testing.T) {
	// An unknown id is a 404 in the representation the client reads, as the success would have been.
	spec := `
openapi: 3.1.0
info: {title: Not found negotiation, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}, example: "1"}
      responses:
        "200":
          description: OK
          content:
            application/json: {example: {id: "1"}}
            application/xml: {example: <pet/>}
        "404":
          description: Not found
          content:
            application/json: {example: {error: missing}}
            application/xml: {example: <missing/>}
`
	m := serveSpec(t, spec)

	tests := []struct {
		accept string
		status int
		mock   string
		body   string
	}{
		{accept: "application/json", status: http.StatusNotFound, mock: "pets-_id-get-404-json", body: `{"error":"missing"}`},
		{accept: "application/xml", status: http.StatusNotFound, mock: "pets-_id-get-404-xml", body: "<missing/>"},
		{accept: "", status: http.StatusNotFound, mock: "pets-_id-get-404-json", body: `{"error":"missing"}`},
		{accept: "text/csv", status: http.StatusNotAcceptable, mock: "pets-_id-get-406"},
	}

	for _, tt := range tests {
		t.Run("Accept "+tt.accept, func(t *testing.T) {
			status, mock, body := call(t, m, http.MethodGet, "/pets/2", tt.accept)
			require.Equal(t, tt.status, status, body)
			require.Equal(t, tt.mock, mock)

			if tt.body != "" {
				require.Equal(t, tt.body, strings.TrimSpace(body))
			}
		})
	}
}

func TestOpenAPI_ABodyOutsideJSONIsTextOrBytes(t *testing.T) {
	// Outside JSON, the generator writes no format: a CSV example given as data has no text to send, so its mock is
	// written disabled and the client gets the 404, and an image given in base64 reaches the client as its bytes.
	spec := `
openapi: 3.1.0
info: {title: Bodies, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}, example: "1"}
      responses:
        "200":
          description: OK
          content:
            text/csv: {example: {id: "1"}}
            image/png:
              schema: {type: string, contentEncoding: base64}
              example: iVBORw0KGgo=
        "404":
          description: Not found
`
	m, logs := serveSpecLogging(t, spec)
	require.Equal(t, `{"level":"warn","mock":"pets-_id-get-200-text-csv-example","method":"GET","route":"/pets/{id}","status":"200",`+
		`"media_type":"text/csv","variant":"example","message":"the body is data, not text: written disabled: give a string example"}`+"\n", logs)

	status, mock, body := call(t, m, http.MethodGet, "/pets/1", "text/csv")
	require.Equal(t, http.StatusNotFound, status, body)
	require.Equal(t, "pets-_id-get-404", mock)

	status, mock, body = call(t, m, http.MethodGet, "/pets/1", "image/png")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "pets-_id-get-200-image-png-example", mock)
	require.Equal(t, "\x89PNG\r\n\x1a\n", body)
}

func TestOpenAPI_ASerializedValueIsServedAsSent(t *testing.T) {
	// A serializedValue is the body as sent, encoding included: text that reads as base64 must reach the client as
	// that text, not as the bytes it decodes to, though its schema says base64.
	spec := `
openapi: 3.2.0
info: {title: Serialized, version: "1"}
paths:
  /encoded:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain:
              schema: {type: string, contentEncoding: base64}
              examples:
                encoded: {serializedValue: aGVsbG8=}
`
	m := serveSpec(t, spec)

	status, _, body := call(t, m, http.MethodGet, "/encoded", "")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "aGVsbG8=", body)
}

func TestOpenAPI_ALiteralValueOfASchemaIsServedAsWritten(t *testing.T) {
	// const and enum hold data, not schemas: a writeOnly key inside one is the value's own, and the body must be
	// exactly that value, or it breaks the schema it was made from.
	spec := `
openapi: 3.1.0
info: {title: Literal values, version: "1"}
paths:
  /const:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                const: {properties: {password: {writeOnly: true, description: literal data}}}
  /enum:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                enum: [{properties: {password: {writeOnly: true}}}]
`
	m := serveSpec(t, spec)

	status, _, body := call(t, m, http.MethodGet, "/const", "")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, `{"properties":{"password":{"writeOnly":true,"description":"literal data"}}}`, body)

	status, _, body = call(t, m, http.MethodGet, "/enum", "")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, `{"properties":{"password":{"writeOnly":true}}}`, body)
}

func TestOpenAPI_AnEncodedLiteralOfAPathMatchesTheRequestThatSendsIt(t *testing.T) {
	// The server compares the path decoded: /hello%20world in a spec is the path a client sends as /hello%20world,
	// in a static path, a template and a path example alike. A % that starts no escape is a percent sign, even beside
	// one that does.
	spec := `
openapi: 3.1.0
info: {title: Encoded literals, version: "1"}
paths:
  /hello%20world:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: found}
  /a%20b/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}, example: "1"}
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: example}
  /c%20d/{id}.json:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: inside}
  /rate/50%:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: half}
  /rate/50%/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: half-of-one}
  /off%2010%:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: ten-off}
`
	m := serveSpec(t, spec)

	tests := []struct {
		path string
		mock string
		body string
	}{
		{path: "/hello%20world", mock: "hello-20world-get-200-text", body: "found"},
		{path: "/a%20b/1", mock: "a-20b-_id-get-200-text-example", body: "example"},
		{path: "/a%20b/2", mock: "a-20b-_id-get-200-text", body: "example"},
		{path: "/c%20d/2.json", mock: "c-20d-_id-json-get-200-text", body: "inside"},
		{path: "/rate/50%25", mock: "rate-50-get-200-text", body: "half"},
		{path: "/rate/50%25/example", mock: "rate-50-_id-get-200-text", body: "half-of-one"},
		{path: "/off%2010%25", mock: "off-2010-get-200-text", body: "ten-off"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, mock, body := call(t, m, http.MethodGet, tt.path, "text/plain")
			require.Equal(t, http.StatusOK, status, body)
			require.Equal(t, tt.mock, mock)
			require.Equal(t, tt.body, body)
		})
	}

	status, _, _ := call(t, m, http.MethodGet, "/hello%2520world", "text/plain")
	require.Equal(t, http.StatusTeapot, status)
}

func TestOpenAPI_TheRootPathIsUnderTheServersPathWithItsSlash(t *testing.T) {
	// A path of the spec is appended to its server's: / under https://example.com/v1 is /v1/, and /v1 is no path the
	// spec declares.
	spec := `
openapi: 3.1.0
info: {title: Root, version: "1"}
servers: [{url: "https://example.com/v1"}]
paths:
  /:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: home}
`
	m := serveSpec(t, spec)

	status, _, body := call(t, m, http.MethodGet, "/v1/", "text/plain")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "home", body)

	status, _, _ = call(t, m, http.MethodGet, "/v1", "text/plain")
	require.Equal(t, http.StatusTeapot, status)
}

func TestOpenAPI_AServerURLIsALinkOfTheSpec(t *testing.T) {
	// A spec served at /docs/openapi.yaml whose server is v1 serves its API at /docs/v1, and a server
	// //api.example.test/v2 names a host, which is no part of the path.
	spec := `
openapi: 3.1.0
info: {title: Relative servers, version: "1"}
servers: [{url: v1}]
paths:
  /x:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: beside-the-spec}
  /y:
    get:
      servers: [{url: "//api.example.test/v2"}]
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: on-its-host}
`
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs/openapi.yaml" {
			http.NotFound(w, r)
			return
		}

		_, _ = io.WriteString(w, spec)
	}))
	t.Cleanup(docs.Close)

	loc, err := url.Parse(docs.URL + "/docs/openapi.yaml")
	require.NoError(t, err)

	m, logs := serveSpecAt(t, loc)
	require.Empty(t, logs)

	tests := []struct {
		path   string
		status int
		body   string
	}{
		{path: "/docs/v1/x", status: http.StatusOK, body: "beside-the-spec"},
		{path: "/v2/y", status: http.StatusOK, body: "on-its-host"},
		{path: "/x", status: http.StatusTeapot},
		{path: "//api.example.test/v2/y", status: http.StatusTeapot},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, _, body := call(t, m, http.MethodGet, tt.path, "text/plain")
			require.Equal(t, tt.status, status, body)

			if tt.body != "" {
				require.Equal(t, tt.body, body)
			}
		})
	}
}

func TestOpenAPI_APathExampleOfAParameterWithContentIsWrittenInItsMediaType(t *testing.T) {
	// A parameter with content is written in its media type: the JSON string "rex" is the value the example answers,
	// and any other the 404.
	spec := `
openapi: 3.1.0
info: {title: Content path example, version: "1"}
paths:
  /pets/{id}:
    get:
      parameters:
        - name: id
          in: path
          required: true
          content:
            application/json:
              schema: {type: string}
              example: rex
      responses:
        "200":
          description: OK
          content:
            text/plain: {example: pet}
        "404":
          description: Missing
`
	m := serveSpec(t, spec)

	status, mock, body := call(t, m, http.MethodGet, "/pets/%22rex%22", "text/plain")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "pets-_id-get-200-text-example", mock)
	require.Equal(t, "pet", body)

	status, mock, body = call(t, m, http.MethodGet, "/pets/%22other%22", "text/plain")
	require.Equal(t, http.StatusNotFound, status, body)
	require.Equal(t, "pets-_id-get-404", mock)
}

func TestOpenAPI_ANullExampleIsTheJSONNull(t *testing.T) {
	// null is a value an example gives, not a missing one: the client gets the JSON null, whether the example is
	// named or not, and when a schema allows it.
	spec := `
openapi: 3.1.0
info: {title: Null example, version: "1"}
paths:
  /named:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              examples:
                null-response: {value: null}
  /schema:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: {type: ["null"]}
              examples:
                null-response: {$ref: "#/components/examples/Nothing"}
  /singular:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: {type: [object, "null"]}
              example: null
components:
  examples:
    Nothing: {value: null}
`
	m := serveSpec(t, spec)

	for _, path := range []string{"/named", "/schema", "/singular"} {
		t.Run(path, func(t *testing.T) {
			res, body := respond(t, m, http.MethodGet, path, "application/json")
			require.Equal(t, http.StatusOK, res.StatusCode, body)
			require.Equal(t, "application/json", res.Header.Get("Content-Type"))
			require.Equal(t, "null", body)
		})
	}
}

func TestOpenAPI_AnIntegerPast2To53IsServedExactly(t *testing.T) {
	// A float64 holds every integer up to 2^53 only: 9007199254740993 read as one is 9007199254740992, another
	// resource, in the path, a header and a body alike, through a reference, and as the const a fake value takes,
	// beside a $ref too.
	spec := `{
  "openapi": "3.1.0",
  "info": {"title": "Big integers", "version": "1"},
  "paths": {
    "/const": {
      "get": {
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"const": 9007199254740993}}}}}
      }
    },
    "/sibling": {
      "get": {
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Integer", "const": 9007199254740993}}}}}
      }
    },
    "/ids/{id}": {
      "get": {
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}, "example": 9007199254740993}],
        "responses": {
          "200": {
            "description": "OK",
            "headers": {"X-ID": {"schema": {"type": "integer"}, "example": 9007199254740993}},
            "content": {"application/json": {"examples": {"id": {"$ref": "#/components/examples/ID"}}}}
          },
          "404": {"description": "missing"}
        }
      }
    }
  },
  "components": {
    "examples": {"ID": {"value": {"id": 9007199254740993}}},
    "schemas": {"Integer": {"type": "integer"}}
  }
}`
	m := serveSpec(t, spec)

	res, body := respond(t, m, http.MethodGet, "/ids/9007199254740993", "application/json")
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, "9007199254740993", res.Header.Get("X-ID"))
	require.JSONEq(t, `{"id":9007199254740993}`, body)

	res, body = respond(t, m, http.MethodGet, "/ids/9007199254740992", "application/json")
	require.Equal(t, http.StatusNotFound, res.StatusCode, body)

	for _, path := range []string{"/const", "/sibling"} {
		res, body = respond(t, m, http.MethodGet, path, "application/json")
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.Equal(t, "9007199254740993", body, path)
	}
}

func TestOpenAPI_AResponseHeaderIsWrittenAsItsSpecSays(t *testing.T) {
	// A header with a schema is in simple style, which explode changes for an object; a header with content is in
	// its media type.
	spec := `
openapi: 3.1.0
info: {title: Headers, version: "1"}
paths:
  /x:
    get:
      responses:
        "204":
          description: OK
          headers:
            X-Color:
              schema: {type: object}
              example: {R: 100, G: 200}
            X-Exploded:
              explode: true
              schema: {type: object}
              example: {R: 100, G: 200}
            X-List:
              schema: {type: array}
              example: [a, b]
            X-Content:
              content:
                application/json:
                  schema: {type: object}
                  example: {R: 100}
`
	m := serveSpec(t, spec)

	res, body := respond(t, m, http.MethodGet, "/x", "")
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, "G,200,R,100", res.Header.Get("X-Color"))
	require.Equal(t, "G=200,R=100", res.Header.Get("X-Exploded"))
	require.Equal(t, "a,b", res.Header.Get("X-List"))
	require.Equal(t, `{"R":100}`, res.Header.Get("X-Content"))
}

func TestOpenAPI_ARequestThatBreaksTheSpecGetsTheErrorItsServerWouldAnswer(t *testing.T) {
	// A client tested against the mocks must meet the errors of the real server: a request without the credential the
	// operation asks gets a 401, a body in a media type it doesn't take a 415, parameters or a body that don't fit
	// their schemas a 400, or the 422 the operation declares, saying what broke, and a method the path lacks a 405.
	// A request of another operation isn't refused by this one's mocks, and a refusal disabled lets the request on.
	spec := `
openapi: 3.1.0
info: {title: Contract, version: "1"}
security: [{bearer: []}]
paths:
  /pets/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: integer, maximum: 100}}
    get:
      responses:
        "200": {description: OK, content: {application/json: {example: {name: Rex}}}}
    put:
      requestBody:
        required: true
        content:
          application/json: {schema: {type: object, required: [name], properties: {name: {type: string}}}}
      responses:
        "204": {description: Saved}
        "422": {description: Invalid, content: {application/json: {example: {message: a pet needs a name}}}}
  /pets/mine:
    get:
      responses:
        "200": {description: OK, content: {application/json: {example: {name: Mine}}}}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
`
	file := filepath.Join(t.TempDir(), "spec.yaml")
	require.NoError(t, os.WriteFile(file, []byte(spec), 0o600))

	loc, err := openapi.Location(file)
	require.NoError(t, err)

	doc, err := openapiv3.Load(t.Context(), []byte(spec), loc, nil, new(zerolog.Nop()))
	require.NoError(t, err)

	files, err := openapi.Generate(t.Context(), doc, new(zerolog.Nop()))
	require.NoError(t, err)

	dir := t.TempDir()
	_, err = openapi.Write(dir, doc.Title, files)
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()).MockFilePatterns(filepath.Join(dir, "*.mock.yaml")).
		Matchers(matchbuild.MustNew(matchopenapi.WithMatcher())))
	m.MustStart()
	t.Cleanup(m.Close)

	send := func(method, path, auth, contentType, body string) (*http.Response, string) {
		req, err := http.NewRequestWithContext(t.Context(), method, m.URL()+path, strings.NewReader(body))
		require.NoError(t, err)

		if auth != "" {
			req.Header.Set("Authorization", auth)
		}

		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		data, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res, string(data)
	}

	tests := []struct {
		name        string
		method      string
		path        string
		auth        string
		contentType string
		body        string
		status      int
		mock        string
		header      map[string]string
		want        string
	}{
		{
			name: "a request that keeps it", method: http.MethodGet, path: "/pets/7", auth: "Bearer t",
			status: http.StatusOK, mock: "pets-_id-get-200-json", want: `{"name":"Rex"}`,
		},
		{
			name: "no credential", method: http.MethodGet, path: "/pets/7",
			status: http.StatusUnauthorized, mock: "pets-_id-get-401",
			header: map[string]string{"WWW-Authenticate": "Bearer", "Content-Type": "application/problem+json"},
			want:   `{"title":"Unauthorized","status":401}`,
		},
		{
			name: "a parameter that breaks it", method: http.MethodGet, path: "/pets/500", auth: "Bearer t",
			status: http.StatusBadRequest, mock: "pets-_id-get-400", header: map[string]string{"Content-Type": "application/problem+json"},
			want: `{"title":"Bad Request","status":400,"errors":["path parameter \"id\": maximum: got 500, want 100"]}`,
		},
		{
			name: "a path of another operation", method: http.MethodGet, path: "/pets/mine", auth: "Bearer t",
			status: http.StatusOK, mock: "pets-mine-get-200-json", want: `{"name":"Mine"}`,
		},
		{
			name: "a body in a media type it doesn't take", method: http.MethodPut, path: "/pets/7", auth: "Bearer t",
			contentType: "text/plain", body: "Rex",
			status: http.StatusUnsupportedMediaType, mock: "pets-_id-put-415", header: map[string]string{"Accept": "application/json"},
			want: `{"title":"Unsupported Media Type","status":415}`,
		},
		{
			name: "a body that breaks it, where the operation declares a 422", method: http.MethodPut, path: "/pets/7",
			auth: "Bearer t", contentType: "application/json", body: `{}`,
			status: http.StatusUnprocessableEntity, mock: "pets-_id-put-422-json", want: `{"message":"a pet needs a name"}`,
		},
		{
			name: "a method the path lacks", method: http.MethodDelete, path: "/pets/7", auth: "Bearer t",
			status: http.StatusMethodNotAllowed, mock: "pets-_id-405", header: map[string]string{"Allow": "GET, HEAD, PUT"},
			want: `{"title":"Method Not Allowed","status":405}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := send(tt.method, tt.path, tt.auth, tt.contentType, tt.body)
			require.Equal(t, tt.status, res.StatusCode, body)
			require.Equal(t, tt.mock, res.Header.Get(vlatte.HeaderMatchedMockID))
			require.JSONEq(t, tt.want, body)

			for name, value := range tt.header {
				require.Equal(t, value, res.Header.Get(name), name)
			}
		})
	}

	// A refusal is a mock like any other: once disabled, the request it refused reaches the success.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, m.AdminURL()+"/mocks/pets-_id-get-400", strings.NewReader(`{"enabled": false}`))
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body := send(http.MethodGet, "/pets/500", "Bearer t", "", "")
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, "pets-_id-get-200-json", res.Header.Get(vlatte.HeaderMatchedMockID))
}

func TestOpenAPI_AnEncodedSlashGetsTheChecksOfThePathItReaches(t *testing.T) {
	// The server compares a literal path, or one with a parameter inside a segment, decoded: /foo%2Fbar reaches the
	// mocks of /foo/bar. The checks of that operation must hold for it too, or a request without credentials gets
	// what only one with them should. A parameter that is a whole segment takes a / sent as %2F.
	m := serveSpec(t, `
openapi: 3.1.0
info: {title: Slashes, version: "1"}
security: [{bearer: []}]
paths:
  /foo/bar:
    get:
      responses: {"200": {description: OK, content: {application/json: {example: {ok: true}}}}}
  /{dir}/{name}.txt:
    get:
      parameters:
        - {name: dir, in: path, required: true, schema: {type: string}}
        - {name: name, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: OK, content: {application/json: {example: {ok: true}}}}}
  /tags/{tag}:
    get:
      parameters:
        - {name: tag, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: OK, content: {application/json: {example: {ok: true}}}}}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
`)

	for _, path := range []string{"/foo/bar", "/foo%2Fbar", "/a/b.txt", "/a%2Fb.txt", "/tags/a%2Fb"} {
		status, _, body := call(t, m, http.MethodGet, path, "")
		require.Equal(t, http.StatusUnauthorized, status, path+": "+body)
	}
}

func TestOpenAPI_AnExampleGetsTheChecksOfItsOperation(t *testing.T) {
	// The mock of an example of a path parameter answers only the requests its operation's route takes, which get
	// the operation's checks: a client sends the example a/b as a%2Fb, and /pets/a/b is no pet's path.
	m := serveSpec(t, `
openapi: 3.1.0
info: {title: Examples, version: "1"}
security: [{bearer: []}]
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}, examples: {slash: {value: a/b}}}
      responses: {"200": {description: OK, content: {application/json: {example: {name: Rex}}}}}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
`)

	send := func(path, auth string) (int, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+path, nil)
		require.NoError(t, err)

		if auth != "" {
			req.Header.Set("Authorization", auth)
		}

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode, res.Header.Get(vlatte.HeaderMatchedMockID)
	}

	status, id := send("/pets/a%2Fb", "")
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "pets-_id-get-401", id)

	status, id = send("/pets/a%2Fb", "Bearer t")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "pets-_id-get-200-json-slash", id)

	status, id = send("/pets/a/b", "Bearer t")
	require.Equal(t, mockhttp.StatusNoMatch, status)
	require.Empty(t, id)
}

func TestOpenAPI_AnEscapeInTheServersPathIsDecodedOnce(t *testing.T) {
	// The server's path /v%2520 is /v%20 decoded once: the mocks of the operation answer under it, and so must its
	// checks, or a request without its required parameter gets the success.
	m := serveSpec(t, `
openapi: 3.1.0
info: {title: Escapes, version: "1"}
servers: [{url: /v%2520}]
paths:
  /input:
    get:
      parameters:
        - {name: limit, in: query, required: true, schema: {type: integer}}
      responses: {"204": {description: OK}}
`)

	status, _, body := call(t, m, http.MethodGet, "/v%2520/input", "")
	require.Equal(t, http.StatusBadRequest, status, body)

	status, _, body = call(t, m, http.MethodGet, "/v%2520/input?limit=1", "")
	require.Equal(t, http.StatusNoContent, status, body)
}

func TestOpenAPI_ARequestNeedNotSendAPropertyAnAllOfBranchMakesReadOnly(t *testing.T) {
	// One branch of an allOf makes id readOnly, another requires it: the object is one, and a client needn't send
	// what the server sets. What a branch requires otherwise still is.
	m := serveSpec(t, `
openapi: 3.1.0
info: {title: Read only, version: "1"}
paths:
  /pets:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              allOf:
                - {type: object, properties: {id: {type: string, readOnly: true}}}
                - {type: object, required: [id, name], properties: {name: {type: string}}}
      responses: {"201": {description: Created}}
`)

	res, body := sendBody(t, m, http.MethodPost, "/pets", "application/json", `{"name":"Rex"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode, body)

	res, body = sendBody(t, m, http.MethodPost, "/pets", "application/json", `{}`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
}

func TestOpenAPI_ABodyIsCheckedAgainstTheValuesItsSchemaWrites(t *testing.T) {
	// A float64 holds every integer up to 2^53 only: an enum or a bound past it, read as one, takes its rounded
	// neighbor, and refuses the value the spec writes. A const of null is a value too. The mocks check a body against
	// the schema as the spec writes it, through the copy of the spec beside them.
	m := serveSpec(t, `{
  "openapi": "3.1.0",
  "info": {"title": "Exact", "version": "1"},
  "paths": {
    "/enum": {"post": {
      "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "integer", "enum": [9007199254740999]}}}},
      "responses": {"204": {"description": "Saved"}}
    }},
    "/maximum": {"post": {
      "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "integer", "maximum": 9007199254740999}}}},
      "responses": {"204": {"description": "Saved"}}
    }},
    "/null": {"post": {
      "requestBody": {"required": true, "content": {"application/json": {"schema": {"const": null}}}},
      "responses": {"204": {"description": "Saved"}}
    }}
  }
}`)

	tests := []struct {
		path   string
		body   string
		status int
		want   string
	}{
		{path: "/enum", body: "9007199254740999", status: http.StatusNoContent},
		{
			path: "/enum", body: "9007199254741000", status: http.StatusBadRequest,
			want: `{"title":"Bad Request","status":400,"errors":["body: value must be 9007199254740999"]}`,
		},
		{path: "/maximum", body: "9007199254740999", status: http.StatusNoContent},
		{
			path: "/maximum", body: "9007199254741000", status: http.StatusBadRequest,
			want: `{"title":"Bad Request","status":400,"errors":["body: maximum: got 9007199254741000, want 9007199254740999"]}`,
		},
		{path: "/null", body: "null", status: http.StatusNoContent},
		{
			path: "/null", body: "{}", status: http.StatusBadRequest,
			want: `{"title":"Bad Request","status":400,"errors":["body: value must be null"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path+" "+tt.body, func(t *testing.T) {
			res, body := sendBody(t, m, http.MethodPost, tt.path, "application/json", tt.body)
			require.Equal(t, tt.status, res.StatusCode, body)

			if tt.want != "" {
				require.JSONEq(t, tt.want, body)
			}
		})
	}
}

func TestOpenAPI_TheStaleMocksOfARemovedOperationStillLoad(t *testing.T) {
	// A rerun keeps the files it no longer writes, which still load: a check of a request against the spec must still
	// find its operation, or the server starts for none of the operations left.
	dir := t.TempDir()

	generate := func(spec string) {
		file := filepath.Join(t.TempDir(), "spec.yaml")
		require.NoError(t, os.WriteFile(file, []byte(spec), 0o600))

		loc, err := openapi.Location(file)
		require.NoError(t, err)
		require.Empty(t, writeMocks(t, dir, loc))
	}

	generate(`
openapi: 3.1.0
info: {title: Rerun, version: "1"}
paths:
  /input:
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
      responses: {"204": {description: OK}}
`)
	generate(`
openapi: 3.1.0
info: {title: Rerun, version: "1"}
paths:
  /current:
    get:
      responses: {"204": {description: OK}}
`)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.yaml")).
		Matchers(matchbuild.MustNew(matchopenapi.WithMatcher())))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	status, _, body := call(t, m, http.MethodGet, "/current", "")
	require.Equal(t, http.StatusNoContent, status, body)

	// The stale check keeps the contract its operation had.
	status, _, body = call(t, m, http.MethodGet, "/input?limit=x", "")
	require.Equal(t, http.StatusBadRequest, status, body)
}

// serveSpec serves the mocks the generator writes of spec, an OpenAPI document, and fails on a warning.
func serveSpec(t *testing.T, spec string) *mockhttp.Server {
	t.Helper()

	m, logs := serveSpecLogging(t, spec)
	require.Empty(t, logs)

	return m
}

// serveSpecLogging serves the mocks the generator writes of spec, an OpenAPI document, and returns what it logs, as
// JSON lines.
func serveSpecLogging(t *testing.T, spec string) (*mockhttp.Server, string) {
	t.Helper()

	file := filepath.Join(t.TempDir(), "spec.yaml")
	require.NoError(t, os.WriteFile(file, []byte(spec), 0o600))

	loc, err := openapi.Location(file)
	require.NoError(t, err)

	return serveSpecAt(t, loc)
}

// serveSpecAt serves the mocks the generator writes of the OpenAPI document at loc, a file or an http(s) URL, and
// returns what it logs, as JSON lines.
func serveSpecAt(t *testing.T, loc *url.URL) (*mockhttp.Server, string) {
	t.Helper()

	mocksDir := t.TempDir()
	logs := writeMocks(t, mocksDir, loc)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(mocksDir, "*.mock.yaml")).
		Matchers(matchbuild.MustNew(matchopenapi.WithMatcher())))
	m.MustStart()
	t.Cleanup(m.Close)

	return m, logs
}

// writeMocks writes to dir the mock files the generator writes of the OpenAPI document at loc, a file or an http(s)
// URL, as the openapi command does, and returns what it logs, as JSON lines.
func writeMocks(t *testing.T, dir string, loc *url.URL) string {
	t.Helper()

	read := func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }

	data, err := read(loc)
	require.NoError(t, err)

	var logs bytes.Buffer

	log := zerolog.New(&logs)
	doc, err := openapiv3.Load(t.Context(), data, loc, read, &log)
	require.NoError(t, err)

	files, err := openapi.Generate(t.Context(), doc, &log)
	require.NoError(t, err)

	_, err = openapi.Write(dir, doc.Title, files)
	require.NoError(t, err)

	return logs.String()
}

// call sends a request to m, with accept as its Accept when not empty, and returns the status, the ID of the mock
// that answered and the body.
func call(t *testing.T, m *mockhttp.Server, method, path, accept string) (int, string, string) {
	t.Helper()

	res, body := respond(t, m, method, path, accept)

	return res.StatusCode, res.Header.Get(vlatte.HeaderMatchedMockID), body
}

// sendBody sends a request of body to m, with contentType as its Content-Type, and returns the response and its body.
func sendBody(t *testing.T, m *mockhttp.Server, method, path, contentType, body string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, m.URL()+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	data, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, string(data)
}

// respond sends a request to m, with accept as its Accept when not empty, and returns the response and its body.
func respond(t *testing.T, m *mockhttp.Server, method, path, accept string) (*http.Response, string) {
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

	return res, string(body)
}
