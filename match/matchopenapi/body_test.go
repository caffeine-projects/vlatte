package matchopenapi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAPI_ChecksABodyAgainstTheValuesItsSchemaWrites(t *testing.T) {
	// A float64 holds every integer up to 2^53 only: an enum or a bound past it, read as one, takes its rounded
	// neighbor, and refuses the value the spec writes. A const of null, which OpenAPI 3.1 allows, is a value too.
	spec := `
openapi: 3.1.0
info: {title: Exact, version: "1"}
paths:
  /enum:
    post:
      requestBody: {required: true, content: {application/json: {schema: {$ref: "#/components/schemas/Enum"}}}}
      responses: {"204": {description: Saved}}
  /maximum:
    post:
      requestBody: {required: true, content: {application/json: {schema: {type: integer, maximum: 9007199254740999}}}}
      responses: {"204": {description: Saved}}
  /null:
    post:
      requestBody: {required: true, content: {application/json: {schema: {const: null}}}}
      responses: {"204": {description: Saved}}
components:
  schemas:
    Enum: {type: integer, enum: [9007199254740999]}
`

	tests := []struct {
		name      string
		operation string
		body      string
		want      []string
		v30       bool // whether OpenAPI 3.0 writes the schema too
	}{
		{name: "the integer of an enum", operation: "POST /enum", body: "9007199254740999", v30: true},
		{
			name: "its rounded neighbor", operation: "POST /enum", body: "9007199254741000", v30: true,
			want: []string{"body: value must be 9007199254740999"},
		},
		{name: "the integer of a maximum", operation: "POST /maximum", body: "9007199254740999", v30: true},
		{
			name: "past it", operation: "POST /maximum", body: "9007199254741000", v30: true,
			want: []string{"body: maximum: got 9007199254741000, want 9007199254740999"},
		},
		{name: "the null of a const", operation: "POST /null", body: "null", v30: true},
		{name: "an object", operation: "POST /null", body: "{}", v30: true, want: []string{"body: value must be null"}},
	}

	for _, version := range []string{"3.0.3", "3.1.0"} {
		v30 := version == "3.0.3"

		s := spec
		if v30 {
			// OpenAPI 3.0 has no const: an enum of null is one.
			s = strings.Replace(spec, "{const: null}", "{nullable: true, enum: [null]}", 1)
		}

		dir := writeSpec(t, strings.Replace(s, "3.1.0", version, 1))

		for _, tt := range tests {
			if v30 && !tt.v30 {
				continue
			}

			t.Run(version+"/"+tt.name, func(t *testing.T) {
				method, path, _ := strings.Cut(tt.operation, " ")

				got, messages := match(t, build(t, dir, tt.operation, "request"), request(t, method, path, "application/json", tt.body))
				require.Equal(t, tt.want == nil, got)
				require.Equal(t, tt.want, messages)
			})
		}
	}
}

func TestOpenAPI_ChecksABodyOtherThanJSONAsKinOpenAPIDoes(t *testing.T) {
	// A form is read as kin-openapi reads it, each field in its schema's type, and a binary string of any media type
	// as the bytes it sends. A media type kin-openapi reads nothing of, such as XML, isn't checked: a server of the
	// spec would read it.
	dir := writeSpec(t, `
openapi: 3.1.0
info: {title: Forms, version: "1"}
paths:
  /pets:
    post:
      requestBody:
        required: true
        content:
          application/x-www-form-urlencoded: {schema: {$ref: "#/components/schemas/Pet"}}
          application/xml: {schema: {$ref: "#/components/schemas/Pet"}}
          image/png: {schema: {type: string, format: binary, maxLength: 3}}
      responses: {"201": {description: Created}}
components:
  schemas:
    Pet:
      type: object
      required: [id, name]
      properties:
        id: {type: integer, readOnly: true}
        name: {type: string}
        age: {type: integer, maximum: 30}
        weight: {type: number, format: int32}
`)
	post := build(t, dir, "POST /pets", "request")

	tests := []struct {
		name        string
		contentType string
		body        string
		want        []string
	}{
		{name: "a form that keeps it", contentType: "application/x-www-form-urlencoded", body: "name=Rex&age=3"},
		{
			name: "a form that breaks it", contentType: "application/x-www-form-urlencoded", body: "age=31&weight=2147483648",
			want: []string{
				"body at /age: maximum: got 31, want 30",
				"body at /weight: 2.147483648e+09 is not valid int32: value should be between -2147483648 and 2147483647",
				"body: missing property 'name'",
			},
		},
		{name: "XML", contentType: "application/xml", body: "<pet/>"},
		{name: "a file that keeps it", contentType: "image/png", body: "abc"},
		{name: "a file that breaks it", contentType: "image/png", body: "abcdef", want: []string{"body: maxLength: got 6, want 3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, messages := match(t, post, request(t, "POST", "/pets", tt.contentType, tt.body))
			require.Equal(t, tt.want == nil, got)
			require.Equal(t, tt.want, messages)
		})
	}
}

func TestOpenAPI_ChecksTheFormatsKinOpenAPIChecks(t *testing.T) {
	// kin-openapi checks the formats byte, date, date-time, int32 and int64, and takes any other, as OpenAPI lets a
	// format be a note: the mocks refuse no more than it did.
	dir := writeSpec(t, `
openapi: 3.1.0
info: {title: Formats, version: "1"}
paths:
  /events:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                day: {type: string, format: date}
                count: {type: integer, format: int32}
                total: {type: integer, format: int64}
                contact: {type: string, format: email}
      responses: {"201": {description: Created}}
`)
	post := build(t, dir, "POST /events", "request")

	got, messages := match(t, post, request(t, "POST", "/events", "application/json", `{"day": "2026-11-08", "count": 1, "contact": "x"}`))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, post, request(t, "POST", "/events", "application/json", `{"day": "tomorrow", "count": 2147483648}`))
	require.False(t, got)
	require.Equal(t, []string{
		`body at /count: 2147483648 is not valid int32: value should be between -2147483648 and 2147483647`,
		`body at /day: 'tomorrow' is not valid date: string doesn't match pattern "^[0-9]{4}-(0[1-9]|10|11|12)-(0[1-9]|[12][0-9]|3[01])$"`,
	}, messages)
	// A number past an int64 is past any integer format, exactly as JSON writes it, however large.
	tests := []struct {
		body string
		want string
	}{
		{body: `{"count": 9223372036854775808}`, want: "body at /count: 9223372036854775808 is not valid int32: value should be between -2147483648 and 2147483647"},
		{body: `{"count": 1e309}`, want: "body at /count: 1e309 is not valid int32: value should be between -2147483648 and 2147483647"},
		{body: `{"total": 9223372036854775808}`, want: "body at /total: 9223372036854775808 is not valid int64: value should be between -9223372036854775808 and 9223372036854775807"},
		{body: `{"total": -9223372036854775809}`, want: "body at /total: -9223372036854775809 is not valid int64: value should be between -9223372036854775808 and 9223372036854775807"},
	}

	for _, tt := range tests {
		got, messages = match(t, post, request(t, "POST", "/events", "application/json", tt.body))
		require.False(t, got, tt.body)
		require.Equal(t, []string{tt.want}, messages)
	}

	got, messages = match(t, post, request(t, "POST", "/events", "application/json", `{"count": 2147483647, "total": 9223372036854775807}`))
	require.True(t, got)
	require.Empty(t, messages)
}

func TestOpenAPI_ChecksABodyAgainstTheBranchItsDiscriminatorNames(t *testing.T) {
	// kin-openapi checks a body of a oneOf, or an anyOf, with a discriminator against the branch it names, as
	// OpenAPI 3.0 has it: the branches a code generator writes, which take each other's bodies, keep their mocks.
	spec := `
openapi: 3.1.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              oneOf: [{$ref: "#/components/schemas/Cat"}, {$ref: "#/components/schemas/Dog"}]
              discriminator: {propertyName: kind, mapping: {cat: "#/components/schemas/Cat", dog: "#/components/schemas/Dog"}}
      responses: {"201": {description: Created}}
components:
  schemas:
    Cat: {type: object, required: [kind], properties: {kind: {type: string}, lives: {type: integer}}}
    Dog: {type: object, required: [kind], properties: {kind: {type: string}, bark: {type: string}}}
`

	tests := []struct {
		name string
		body string
		want []string
	}{
		{name: "a body both branches take", body: `{"kind": "cat", "lives": 9}`},
		{name: "a body the named branch takes", body: `{"kind": "cat", "lives": 9, "bark": 1}`},
		{name: "a body it breaks", body: `{"kind": "dog", "bark": 1}`, want: []string{"body at /bark: got number, want string"}},
		{name: "a value it doesn't map", body: `{"kind": "cow"}`, want: []string{"body at /kind: value must be one of 'cat', 'dog'"}},
		{name: "no value", body: `{"lives": 9}`, want: []string{"body: missing property 'kind'"}},
	}

	for _, version := range []string{"3.0.3", "3.1.0"} {
		post := build(t, writeSpec(t, strings.Replace(spec, "3.1.0", version, 1)), "POST /pets", "request")

		for _, tt := range tests {
			t.Run(version+"/"+tt.name, func(t *testing.T) {
				got, messages := match(t, post, request(t, "POST", "/pets", "application/json", tt.body))
				require.Equal(t, tt.want == nil, got)
				require.Equal(t, tt.want, messages)
			})
		}
	}
}

func TestOpenAPI_TakesNullForAnOpenAPI30SchemaThatIsNullable(t *testing.T) {
	// kin-openapi takes null for a nullable schema of OpenAPI 3.0 before any other keyword: its enum, or the allOf
	// that nullable references are written with.
	dir := writeSpec(t, `
openapi: 3.0.3
info: {title: Nullable, version: "1"}
paths:
  /pets:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                size: {type: string, nullable: true, enum: [small, big]}
                owner: {nullable: true, allOf: [{$ref: "#/components/schemas/Owner"}]}
      responses: {"201": {description: Created}}
components:
  schemas:
    Owner: {type: object, required: [name], properties: {name: {type: string}}}
`)
	post := build(t, dir, "POST /pets", "request")

	got, messages := match(t, post, request(t, "POST", "/pets", "application/json", `{"size": null, "owner": null}`))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, post, request(t, "POST", "/pets", "application/json", `{"size": "huge", "owner": {}}`))
	require.False(t, got)
	require.Equal(t, []string{"body at /owner: missing property 'name'", "body at /size: value must be one of 'small', 'big', null"}, messages)
}

func TestOpenAPI_ChecksAnOpenAPI30NumberOfAnIntegerFormatAsKinOpenAPIDoes(t *testing.T) {
	// kin-openapi checks an integer format, such as int32, of an OpenAPI 3.0 schema of type integer only.
	dir := writeSpec(t, `
openapi: 3.0.3
info: {title: Formats, version: "1"}
paths:
  /events:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                count: {type: integer, format: int32}
                total: {type: number, format: int32}
      responses: {"201": {description: Created}}
`)
	post := build(t, dir, "POST /events", "request")

	got, messages := match(t, post, request(t, "POST", "/events", "application/json", `{"count": 1, "total": 3000000000}`))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, post, request(t, "POST", "/events", "application/json", `{"count": 3000000000}`))
	require.False(t, got)
	require.Equal(t, []string{"body at /count: 3000000000 is not valid int32: value should be between -2147483648 and 2147483647"}, messages)
}

func TestOpenAPI_ChecksWithKinOpenAPIABodyWhoseSchemaDoesNotCompile(t *testing.T) {
	// A schema that refers into the paths of the spec doesn't compile as JSON Schema: kin-openapi checks its bodies,
	// as it did before.
	dir := writeSpec(t, `
openapi: 3.1.0
info: {title: Notes, version: "1"}
paths:
  /plain:
    post:
      requestBody: {content: {text/plain: {schema: {type: string, maxLength: 3}}}}
      responses: {"204": {description: Saved}}
  /notes:
    post:
      requestBody:
        required: true
        content:
          application/json: {schema: {$ref: "#/paths/~1plain/post/requestBody/content/text~1plain/schema"}}
      responses: {"204": {description: Saved}}
`)
	post := build(t, dir, "POST /notes", "request")

	got, messages := match(t, post, request(t, "POST", "/notes", "application/json", `"abc"`))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, post, request(t, "POST", "/notes", "application/json", `"abcdef"`))
	require.False(t, got)
	require.Equal(t, []string{"body: maximum string length is 3"}, messages)
}

func TestOpenAPI_LeavesUncheckedAPatternGoCannotCompile(t *testing.T) {
	// A pattern Go can't compile, such as one that looks ahead, can't be checked: the rest of the body still is.
	dir := writeSpec(t, `
openapi: 3.1.0
info: {title: Tags, version: "1"}
paths:
  /tags:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name: {type: string, pattern: "^(?!x)"}
                size: {maximum: 1}
              patternProperties: {"^(?!x)": false}
      responses: {"201": {description: Created}}
`)
	post := build(t, dir, "POST /tags", "request")

	got, messages := match(t, post, request(t, "POST", "/tags", "application/json", `{"name": "x"}`))
	require.True(t, got)
	require.Empty(t, messages)

	got, messages = match(t, post, request(t, "POST", "/tags", "application/json", `{"name": "x", "size": 2}`))
	require.False(t, got)
	require.Equal(t, []string{"body at /size: maximum: got 2, want 1"}, messages)
}
