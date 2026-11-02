// Package openapi writes mock files from an OpenAPI description. It reads a [Document], a model that is the same
// for every version of OpenAPI, which the version packages fill: openapiv3 from OpenAPI 3.0, 3.1 and 3.2. It imports
// none of them, nor any OpenAPI library.
//
// [Generate] turns each operation of a document into mocks in priority bands, above the priority 0 of mocks written
// by hand, so those always win:
//
//   - 100 + s: the responses nothing in a request predicts, such as 403 or 503, written disabled. Enabled through
//     the admin API, one answers before the others.
//   - 200 + s: a mock for each example of the path parameters, on the concrete path.
//   - 300 + s: the main success response, one mock for each media type it offers.
//   - 400 + s: a 406 for a request whose Accept takes none of those media types.
//   - 500 + s: a 404, when the spec declares one and the path parameters have examples.
//
// s is the number of templated segments of the path, so /pets/mine matches before /pets/{id}. The statuses that a
// request's contract decides, 400, 401, 405, 415 and 422, get no mock.
//
// A rerun on the same document writes the same bytes: the operations are sorted, and fake values are seeded by the
// mock they belong to.
package openapi

import "errors"

var (
	// ErrNotOpenAPI is the error of a document that names no version of OpenAPI or Swagger.
	ErrNotOpenAPI = errors.New("openapi: not an OpenAPI or Swagger document")
	// ErrInvalid is the error of a spec its version package finds invalid: mocks of it would mislead.
	ErrInvalid = errors.New("openapi: invalid spec")
	// ErrTooLarge is the error of a spec, or a file it refers to, larger than [MaxSpecSize].
	ErrTooLarge = errors.New("openapi: the spec is too large")
	// ErrNotGenerated is the error of a file [Write] would overwrite that it didn't write from the same spec.
	ErrNotGenerated = errors.New("openapi: a file to write is not one generated from this spec")
)

// Document is an OpenAPI description, in the terms every version shares. Its schemas are JSON Schema 2020-12
// documents, maps or booleans, whose $ref values point into Defs as #/$defs/<name>.
type Document struct {
	// Title is the title of the API. Generated files name it, so a rerun knows them.
	Title string

	// BasePath is the path every operation's path is under, such as /v1, or "" for none.
	BasePath string

	// Operations are the operations of the API, in any order.
	Operations []Operation

	// Defs are the schemas the others share, by name.
	Defs map[string]any

	// Warnings are what the version package read but could not honor, such as servers with different paths.
	Warnings []string
}

// Operation is an HTTP method on a path.
type Operation struct {
	// Method is the HTTP method, in upper case, such as GET or QUERY.
	Method string

	// Path is the path, as the spec writes it, such as /pets/{id}.
	Path string

	// Summary describes the operation in a line. It may be empty.
	Summary string

	// PathParams are the parameters of Path.
	PathParams []Parameter

	// Responses are the responses the operation declares.
	Responses []Response
}

// Parameter is a parameter of a path.
type Parameter struct {
	// Name is the name the path gives it, between braces.
	Name string

	// Examples are values of the parameter, the parameter's own examples first, then its schema's.
	Examples []Example
}

// Response is a response an operation declares.
type Response struct {
	// Status is the key of the response: a status such as 200, a range such as 4XX, or default.
	Status string

	// Headers are the headers of the response, but Content-Type.
	Headers []Header

	// Content holds the body of the response for each media type it offers, in order of preference.
	Content []Content
}

// Content is the body of a response in a media type.
type Content struct {
	// MediaType is the media type, such as application/json.
	MediaType string

	// Schema is the schema of the body, or nil for none.
	Schema any

	// Examples are bodies the spec gives for the media type.
	Examples []Example
}

// Header is a header of a response.
type Header struct {
	// Name is the name of the header.
	Name string

	// Schema is the schema of its value, or nil for none.
	Schema any

	// Examples are values the spec gives for it.
	Examples []Example
}

// Example is a value a spec gives as an example.
type Example struct {
	// Name is the name the spec gives the example, or "" for an unnamed one.
	Name string

	// Value is the example's value.
	Value any
}
