package matchopenapi_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/match/matchopenapi"
)

// _pets is a spec as vlatte openapi copies it: every reference local, and each operation under one server, of its
// base path.
const _pets = `
openapi: 3.1.0
info: {title: Pets, version: "1"}
security: [{bearer: []}]
paths:
  /pets/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string, format: uuid}}
    get:
      operationId: getPet
      servers: [{url: /v1}]
      parameters:
        - {name: limit, in: query, schema: {type: integer, maximum: 100}}
        - {name: X-Trace, in: header, required: true, schema: {type: string}}
        - {name: tags, in: query, style: form, explode: false, schema: {type: array, items: {type: integer}}}
      responses: {"200": {description: OK}}
    put:
      servers: [{url: /v1}]
      security: [{key: [], basic: []}, {oauth: [write]}, {}]
      requestBody:
        required: true
        content:
          application/json: {schema: {$ref: "#/components/schemas/Pet"}}
          application/xml: {schema: {$ref: "#/components/schemas/Pet"}}
      responses: {"204": {description: Saved}}
  /owners:
    post:
      security: []
      requestBody:
        required: true
        content:
          application/json: {schema: {$ref: "#/components/schemas/Owner"}}
      responses: {"201": {description: Created}}
  /pets/mine:
    get:
      servers: [{url: /v1}]
      responses: {"200": {description: OK}}
  /tags/{tag}:
    get:
      security: []
      parameters:
        - {name: tag, in: path, required: true, schema: {type: string, pattern: "^(?!x)"}}
      responses: {"200": {description: OK}}
  /files/{name}.{ext}:
    get:
      security: [{key: []}, {cookie: []}, {query: []}, {tls: []}]
      parameters:
        - {name: name, in: path, required: true, schema: {type: string, maxLength: 3}}
        - {name: ext, in: path, required: true, schema: {type: string, enum: [txt]}}
      responses: {"200": {description: OK}}
components:
  schemas:
    Pet:
      type: object
      required: [id, name]
      properties:
        id: {type: string, readOnly: true}
        name: {type: string}
        age: {type: integer, minimum: 0}
        tags: {type: array, items: {type: object, properties: {name: {type: string}}}}
    Owner:
      allOf: [{properties: {id: {type: string, readOnly: true}}}]
      required: [id, name]
      properties:
        name: {type: string}
    Loop:
      allOf: [{$ref: "#/components/schemas/Loop"}]
      required: [id]
  securitySchemes:
    bearer: {type: http, scheme: bearer}
    basic: {type: http, scheme: basic}
    key: {type: apiKey, in: header, name: X-API-Key}
    cookie: {type: apiKey, in: cookie, name: session}
    query: {type: apiKey, in: query, name: api_key}
    oauth: {type: oauth2, flows: {implicit: {authorizationUrl: "https://example.com/auth", scopes: {write: Write}}}}
    tls: {type: mutualTLS}
`

const _id = "11111111-1111-4111-8111-111111111111"

func TestOpenAPI_MatchesARequestWhoseParametersAndBodyKeepTheSpec(t *testing.T) {
	// kin-openapi reads each parameter in its style and type, as a server of the spec does: a number in a query is a
	// number, and a list is its items. A client may send back a readOnly property, and needn't send a required one.
	// kin-openapi words what a 3.0 spec and a 3.1 one break apart: the explanation says it in one line each, sorted.
	tests := []struct {
		name      string
		operation string
		req       func() *http.Request
		want30    []string
		want31    []string
	}{
		{name: "a request that keeps it", operation: "getPet", req: func() *http.Request {
			return withHeader(request(t, "GET", "/v1/pets/"+_id+"?limit=5&tags=1,2", "", ""), "X-Trace", "a")
		}},
		{
			name: "parameters that break it", operation: "getPet",
			req: func() *http.Request { return request(t, "GET", "/v1/pets/"+_id+"?limit=500&tags=1,a", "", "") },
			want30: []string{
				`query parameter "limit": number must be at most 100`,
				`header parameter "X-Trace": value is required but missing`,
				`query parameter "tags": path 1: value a: an invalid integer: invalid syntax`,
			},
			want31: []string{
				`query parameter "limit": maximum: got 500, want 100`,
				`header parameter "X-Trace": value is required but missing`,
				`query parameter "tags": path 1: value a: an invalid integer: invalid syntax`,
			},
		},
		{name: "a body that keeps it", operation: "PUT /v1/pets/{id}", req: func() *http.Request {
			return request(t, "PUT", "/v1/pets/"+_id, "application/json", `{"id": "1", "name": "Rex", "age": 3}`)
		}},
		{
			name: "a body that breaks it", operation: "PUT /v1/pets/{id}",
			req: func() *http.Request {
				return request(t, "PUT", "/v1/pets/"+_id, "application/json", `{"age": -1, "tags": [{"name": 1}]}`)
			},
			want30: []string{
				`body at /age: number must be at least 0`,
				`body at /name: property "name" is missing`,
				`body at /tags/0/name: value must be a string`,
			},
			want31: []string{
				`body at /age: minimum: got -1, want 0`,
				`body at /tags/0/name: got number, want string`,
				`body: missing property 'name'`,
			},
		},
		{
			name: "a body that leaves out a readOnly property of a schema of its allOf", operation: "POST /owners",
			req:    func() *http.Request { return request(t, "POST", "/owners", "application/json", `{}`) },
			want30: []string{`body at /name: property "name" is missing`},
			want31: []string{`body: missing property 'name'`},
		},
		{
			name: "a body that isn't JSON", operation: "PUT /v1/pets/{id}",
			req:    func() *http.Request { return request(t, "PUT", "/v1/pets/"+_id, "application/json", `{`) },
			want30: []string{`body: failed to decode request body: unexpected EOF`},
			want31: []string{`body: failed to decode request body: unexpected EOF`},
		},
		{
			name: "no body", operation: "PUT /v1/pets/{id}",
			req:    func() *http.Request { return request(t, "PUT", "/v1/pets/"+_id, "", "") },
			want30: []string{"body: value is required but missing"},
			want31: []string{"body: value is required but missing"},
		},
		{
			name: "a body in a media type it doesn't take", operation: "PUT /v1/pets/{id}",
			req:    func() *http.Request { return request(t, "PUT", "/v1/pets/"+_id, "text/plain", "Rex") },
			want30: []string{`body: header Content-Type has unexpected value "text/plain"`},
			want31: []string{`body: header Content-Type has unexpected value "text/plain"`},
		},
		// kin-openapi reads no XML: a server would.
		{name: "a body in a media type kin-openapi can't read", operation: "PUT /v1/pets/{id}", req: func() *http.Request {
			return request(t, "PUT", "/v1/pets/"+_id, "application/xml", "<pet/>")
		}},
	}

	for _, version := range []string{"3.0.3", "3.1.0"} {
		dir := writeSpec(t, strings.Replace(_pets, "3.1.0", version, 1))

		for _, tt := range tests {
			t.Run(version+"/"+tt.name, func(t *testing.T) {
				want := tt.want31
				if version == "3.0.3" {
					want = tt.want30
				}

				req := tt.req()

				got, messages := match(t, build(t, dir, tt.operation, "request"), req)
				require.Equal(t, want == nil, got)
				require.Equal(t, want, messages)

				// The server reads the body after its matchers, as a proxy mock does to forward it: it is left whole.
				if req.GetBody != nil {
					sent, err := req.GetBody()
					require.NoError(t, err)

					want, err := io.ReadAll(sent)
					require.NoError(t, err)

					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, string(want), string(body))
				}
			})
		}
	}
}

func TestOpenAPI_LeavesUncheckedWhatItCannotRead(t *testing.T) {
	// A body the server didn't keep to read again, such as one past its parsing limit, can't be read without taking
	// it from the reply, and a pattern Go can't compile can't be checked: neither refuses a request.
	put := build(t, writeSpec(t, _pets), "PUT /v1/pets/{id}", "request")

	req := request(t, "PUT", "/v1/pets/"+_id, "application/json", `{"age": -1}`)
	req.GetBody = nil

	got, messages := match(t, put, req)
	require.True(t, got)
	require.Empty(t, messages)

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"age": -1}`, string(body))

	// Nor is one that GetBody fails to give again.
	req = request(t, "PUT", "/v1/pets/"+_id, "application/json", `{"age": -1}`)
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("gone") }

	got, messages = match(t, put, req)
	require.True(t, got)
	require.Empty(t, messages)

	for _, version := range []string{"3.0.3", "3.1.0"} {
		t.Run(version, func(t *testing.T) {
			tags := build(t, writeSpec(t, strings.Replace(_pets, "3.1.0", version, 1)), "GET /tags/{tag}", "request")

			got, messages := match(t, tags, request(t, "GET", "/tags/x", "", ""))
			require.True(t, got)
			require.Empty(t, messages)
		})
	}
}

func TestOpenAPI_MatchesARequestWithTheCredentialsOfARequirement(t *testing.T) {
	// A requirement needs each of its schemes, and a request keeps one of the requirements: whether the credential
	// is good is the reply's to say, not the contract's.
	put := build(t, writeSpec(t, strings.Replace(_pets, ", {}]", "]", 1)), "PUT /v1/pets/{id}", "security")
	get := build(t, writeSpec(t, _pets), "getPet", "security")
	files := build(t, writeSpec(t, _pets), "GET /files/{name}.{ext}", "security")
	owners := build(t, writeSpec(t, _pets), "POST /owners", "security")
	// A spec written by hand may name a type of scheme OpenAPI lacks.
	magic := build(t, writeSpec(t, strings.Replace(_pets, "{type: mutualTLS}", "{type: magic}", 1)), "GET /files/{name}.{ext}", "security")

	missing := []string{"security: expected an Authorization header of scheme basic and the header X-API-Key, or an Authorization header"}

	cert := request(t, "GET", "/files/a.txt", "", "")
	cert.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}

	tests := []struct {
		name    string
		matcher vlatte.Matcher
		req     *http.Request
		want    []string
	}{
		{name: "both schemes of a requirement", matcher: put, req: withHeader(withHeader(request(t, "PUT", "/v1/pets/"+_id, "", ""), "Authorization", "basic YTpi"), "X-API-Key", "k")},
		{name: "one scheme of a requirement", matcher: put, req: withHeader(request(t, "PUT", "/v1/pets/"+_id, "", ""), "X-API-Key", "k"), want: []string{
			"security: expected an Authorization header of scheme basic, or an Authorization header",
		}},
		{name: "another requirement", matcher: put, req: withHeader(request(t, "PUT", "/v1/pets/"+_id, "", ""), "Authorization", "Bearer t")},
		{name: "none", matcher: put, req: request(t, "PUT", "/v1/pets/"+_id, "", ""), want: missing},
		{name: "the spec's requirement", matcher: get, req: withHeader(request(t, "GET", "/v1/pets/"+_id, "", ""), "Authorization", "Bearer t")},
		{name: "another scheme than the spec's", matcher: get, req: withHeader(request(t, "GET", "/v1/pets/"+_id, "", ""), "Authorization", "Basic YTpi"), want: []string{
			"security: expected an Authorization header of scheme bearer",
		}},
		{name: "a cookie", matcher: files, req: withHeader(request(t, "GET", "/files/a.txt", "", ""), "Cookie", "session=s")},
		{name: "a query parameter", matcher: files, req: request(t, "GET", "/files/a.txt?api_key=k", "", "")},
		{name: "a client certificate", matcher: files, req: cert},
		{name: "none of them", matcher: files, req: request(t, "GET", "/files/a.txt", "", ""), want: []string{
			"security: expected the header X-API-Key, or the cookie session, or the query parameter api_key, or a client certificate",
		}},
		{name: "an operation that requires none", matcher: owners, req: request(t, "POST", "/owners", "", "")},
		{name: "a scheme of a type it doesn't know", matcher: magic, req: request(t, "GET", "/files/a.txt", "", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, messages := match(t, tt.matcher, tt.req)
			require.Equal(t, tt.want == nil, got)
			require.Equal(t, tt.want, messages)
		})
	}

	// An empty requirement takes a request without credentials.
	got, messages := match(t, build(t, writeSpec(t, _pets), "PUT /v1/pets/{id}", "security"), request(t, "PUT", "/v1/pets/"+_id, "", ""))
	require.True(t, got)
	require.Empty(t, messages)
}

func TestOpenAPI_MatchesABodyInAMediaTypeTheOperationTakes(t *testing.T) {
	m := build(t, writeSpec(t, _pets), "PUT /v1/pets/{id}", "content_type")

	tests := []struct {
		name string
		req  *http.Request
		want []string
	}{
		{name: "a media type it takes", req: request(t, "PUT", "/v1/pets/"+_id, "application/json; charset=utf-8", "{}")},
		{name: "another", req: request(t, "PUT", "/v1/pets/"+_id, "text/plain", "a"), want: []string{
			`content type "text/plain": expected one of: application/json, application/xml`,
		}},
		{name: "none", req: request(t, "PUT", "/v1/pets/"+_id, "", "a"), want: []string{`content type "": expected one of: application/json, application/xml`}},
		// A body that is missing is the request check's to refuse.
		{name: "no body", req: request(t, "PUT", "/v1/pets/"+_id, "text/plain", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, messages := match(t, m, tt.req)
			require.Equal(t, tt.want == nil, got)
			require.Equal(t, tt.want, messages)
		})
	}
}

func TestOpenAPI_MatchesARequestOfAnotherOperation(t *testing.T) {
	// OpenAPI matches the path with fewer parameters first: GET /v1/pets/mine is not a request for a pet whose id
	// isn't a UUID. Nor is a request of another method or path one of the operation's.
	m := build(t, writeSpec(t, _pets), "getPet", "request")

	for _, req := range []*http.Request{
		request(t, "GET", "/v1/pets/mine", "", ""),
		request(t, "PUT", "/v1/pets/mine-x", "", ""),
		request(t, "GET", "/v2/pets/mine-x", "", ""),
		request(t, "GET", "/v1/pets/mine-x/a", "", ""),
	} {
		got, messages := match(t, m, req)
		require.True(t, got, req.Method+" "+req.URL.Path)
		require.Empty(t, messages)
	}

	// OpenAPI matches the path before the method: PUT /v1/pets/mine is a method /pets/mine lacks, which its 405
	// answers, not a pet to save without a body.
	got, messages := match(t, build(t, writeSpec(t, _pets), "PUT /v1/pets/{id}", "request"), request(t, "PUT", "/v1/pets/mine", "", ""))
	require.True(t, got)
	require.Empty(t, messages)

	// A path parameter is decoded, and may be part of a segment.
	got, messages = match(t, build(t, writeSpec(t, _pets), "GET /files/{name}.{ext}", "request"), request(t, "GET", "/files/a%20b.txt", "", ""))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, build(t, writeSpec(t, _pets), "GET /files/{name}.{ext}", "request"), request(t, "GET", "/files/a%20bcd.md", "", ""))
	require.False(t, got)
	require.Equal(t, []string{`path parameter "name": maxLength: got 5, want 3`, `path parameter "ext": value must be 'txt'`}, messages)

	// A segment that doesn't fit the template is of another path: /files/a has no extension to check.
	got, messages = match(t, build(t, writeSpec(t, _pets), "GET /files/{name}.{ext}", "request"), request(t, "GET", "/files/a", "", ""))
	require.True(t, got)
	require.Empty(t, messages)
}

func TestOpenAPI_RefusesArgumentsItCannotUse(t *testing.T) {
	// A mock file whose matcher can't check anything doesn't load: it would refuse every request, or none.
	dir := writeSpec(t, _pets)
	spec := filepath.Join(dir, "pets.openapi.yaml")
	folder := filepath.Join(dir, "folder")
	require.NoError(t, os.Mkdir(folder, 0o700))

	tests := []struct {
		name string
		args []any
		want string
	}{
		{name: "no object", args: []any{"openapi", "pets.openapi.yaml"}, want: "openapi: expected one object of spec, operation and check, not [pets.openapi.yaml]"},
		{name: "an unknown key", args: []any{"openapi", map[string]any{"spec": "pets.openapi.yaml", "operation": "getPet", "check": "request", "mode": "x"}}, want: `openapi: unknown key "mode": expected spec, operation and check`},
		{name: "no spec", args: []any{"openapi", map[string]any{"operation": "getPet", "check": "request"}}, want: "openapi: spec: expected the file of a spec, not <nil>"},
		{name: "no operation", args: []any{"openapi", map[string]any{"spec": "pets.openapi.yaml", "check": "request"}}, want: "openapi: operation: expected an operationId, or a method and a path, not <nil>"},
		{name: "another check", args: []any{"openapi", map[string]any{"spec": "pets.openapi.yaml", "operation": "getPet", "check": "all"}}, want: "openapi: check: expected security, content_type or request, not all"},
		{name: "an operation the spec lacks", args: []any{"openapi", map[string]any{"spec": "pets.openapi.yaml", "operation": "GET /pets/{id}", "check": "request"}}, want: `openapi: operation: no operation "GET /pets/{id}" in ` + spec + ": expected an operationId, or a method and a path, such as GET /pets"},
		{name: "a missing spec", args: []any{"openapi", map[string]any{"spec": "none.yaml", "operation": "getPet", "check": "request"}}, want: "openapi: spec: reading " + filepath.Join(dir, "none.yaml") + ": stat " + filepath.Join(dir, "none.yaml") + ": no such file or directory"},
		{name: "a folder", args: []any{"openapi", map[string]any{"spec": "folder", "operation": "getPet", "check": "request"}}, want: "openapi: spec: reading " + folder + ": read " + folder + ": is a directory"},
	}

	r := matchbuild.MustNew(matchopenapi.WithMatcher()).InDir(dir)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.BuildMatcher(tt.args)
			require.EqualError(t, err, tt.want)
		})
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("openapi: 3.1.0\npaths: {/a: {get: {responses: {'200': {$ref: '#/nowhere'}}}}}\n"), 0o600))

	_, err := r.BuildMatcher([]any{"openapi", map[string]any{"spec": "bad.yaml", "operation": "GET /a", "check": "request"}})
	require.ErrorContains(t, err, "openapi: spec: loading "+filepath.Join(dir, "bad.yaml")+": ")
}

func TestOpenAPI_DescribesItselfWithItsSpecAbsolute(t *testing.T) {
	// The admin API lists a mock by its description, which may load back from another folder.
	dir := writeSpec(t, _pets)

	m := build(t, dir, "getPet", "security")
	require.Equal(t, vlatte.DescribeSingleArg("openapi", map[string]any{
		"spec": filepath.Join(dir, "pets.openapi.yaml"), "operation": "getPet", "check": "security",
	}), m.(vlatte.SelfDescriptive).Describe())
	require.Equal(t, "OpenAPI", m.Name())
}

func TestOpenAPI_LoadsTheSpecAgainOnceItChanges(t *testing.T) {
	// A rerun of vlatte openapi rewrites the spec beside the mocks it rewrites: they must check against the new one.
	dir := writeSpec(t, _pets)
	r := matchbuild.MustNew(matchopenapi.WithMatcher()).InDir(dir)
	args := func(operation string) []any {
		return []any{"openapi", map[string]any{"spec": "pets.openapi.yaml", "operation": operation, "check": "request"}}
	}

	_, err := r.BuildMatcher(args("getPet"))
	require.NoError(t, err)

	path := filepath.Join(dir, "pets.openapi.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(_pets, "getPet", "readPet", 1)), 0o600))
	require.NoError(t, os.Chtimes(path, time.Time{}, time.Now().Add(time.Hour)))

	_, err = r.BuildMatcher(args("readPet"))
	require.NoError(t, err)

	_, err = r.BuildMatcher(args("getPet"))
	require.ErrorContains(t, err, `no operation "getPet"`)
}

func TestOpenAPI_DoesNotMatchAnythingButARequest(t *testing.T) {
	// Under not, a matcher that can't say a value keeps the contract makes its mock refuse it: a mistake shows.
	exp := vlatte.NewExplanation()

	got, err := build(t, writeSpec(t, _pets), "getPet", "request").Match(vlatte.NewMatcherArg(t.Context(), exp, "GET /v1/pets"))
	require.NoError(t, err)
	require.False(t, got)
	require.Equal(t, []string{"openapi: expected a request, under request.custom, not string"}, exp.Messages())
}

// writeSpec writes spec to pets.openapi.yaml in a new folder, and returns the folder.
func writeSpec(t *testing.T, spec string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pets.openapi.yaml"), []byte(spec), 0o600))

	return dir
}

// build builds the openapi matcher of the operation and check of the spec in dir.
func build(t *testing.T, dir, operation, check string) vlatte.Matcher {
	t.Helper()

	m, err := matchbuild.MustNew(matchopenapi.WithMatcher()).InDir(dir).BuildMatcher([]any{
		"openapi", map[string]any{"spec": "pets.openapi.yaml", "operation": operation, "check": check},
	})
	require.NoError(t, err)

	return m
}

// request returns a request as the server gives it to matchers: its body, when it has one, read again with GetBody.
func request(t *testing.T, method, target, contentType, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}

	if body != "" {
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
	}

	return r
}

func withHeader(r *http.Request, name, value string) *http.Request {
	r.Header.Add(name, value)
	return r
}

// match matches r with m, and returns whether it matched and the messages m recorded.
func match(t *testing.T, m vlatte.Matcher, r *http.Request) (bool, []string) {
	t.Helper()

	exp := vlatte.NewExplanation()
	got, err := m.Match(vlatte.NewMatcherArg(t.Context(), exp, r))
	require.NoError(t, err)

	return got, exp.Messages()
}
