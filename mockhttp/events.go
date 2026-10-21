package mockhttp

import (
	"net/http"
	"net/url"
	"time"
)

// RequestValues groups HTTP request data, including the parsed body.
// It is used by several components during the request matching phase.
type RequestValues struct {
	// ReqID identifies the request (e.g. for logging). Set by the request parser.
	ReqID string

	// StartedAtTime indicates when the request arrived.
	StartedAtTime time.Time

	// RawRequest is the original incoming http.Request.
	RawRequest *http.Request

	// URL is the full request url.URL, including scheme, host, port: those the request names, when a forward proxy
	// gets or intercepts it, and the server's otherwise.
	URL *url.URL

	// URLPathSegments stores path segments, each decoded: an escaped "/" stays in its segment.
	// Eg.: /test/100 -> []string{"test", "100"}, and /files/a%2Fb -> []string{"files", "a/b"}
	URLPathSegments []string

	// RawBodyBytes is the HTTP request body bytes.
	RawBodyBytes []byte

	// ParsedBodyData is the parsed http.Request body parsed by a RequestBodyParser instance.
	// It'll be nil if the HTTP request does not contain a body.
	ParsedBodyData any

	// App exposes application instance associated with the incoming HTTP request.
	App *Server

	// Mock is the matched Mock for the current HTTP request.
	Mock *Mock

	Data any

	// bodyParser is the parser that read the body, or nil.
	bodyParser RequestBodyParser
}

// Accessors for the fields above.
func (r *RequestValues) ID() string                     { return r.ReqID }
func (r *RequestValues) StartedAt() time.Time           { return r.StartedAtTime }
func (r *RequestValues) OriginalRequest() *http.Request { return r.RawRequest }
func (r *RequestValues) RawBody() []byte                { return r.RawBodyBytes }
func (r *RequestValues) ParsedBody() any                { return r.ParsedBodyData }
