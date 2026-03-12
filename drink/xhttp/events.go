package xhttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Req[*http.Request] = (*RequestValues)(nil)

// RequestValues groups HTTP request data, including the parsed body.
// It is used by several components during the request matching phase.
// RequestValues implements vlatte.Req[*http.Request].
type RequestValues struct {
	// ReqID identifies the request (e.g. for logging). Set by the request parser.
	ReqID string

	// StartedAtTime indicates when the request arrived.
	StartedAtTime time.Time

	// RawRequest is the original incoming http.Request.
	RawRequest *http.Request

	// URL is the full request url.URL, including scheme, host, port.
	URL *url.URL

	// URLPathSegments stores path segments.
	// Eg.: /test/100 -> []string{"test", "100"}
	URLPathSegments []string

	// RawBodyBytes is the HTTP request body bytes.
	RawBodyBytes []byte

	// ParsedBodyData is the parsed http.Request body parsed by a RequestBodyParser instance.
	// It'll be nil if the HTTP request does not contain a body.
	ParsedBodyData any

	// App exposes application instance associated with the incoming HTTP request.
	App *HTTPMockApp

	// Mock is the matched Mock for the current HTTP request.
	Mock *HTTPMock

	Data any
}

// Req interface implementation.
func (r *RequestValues) ID() string                     { return r.ReqID }
func (r *RequestValues) StartedAt() time.Time           { return r.StartedAtTime }
func (r *RequestValues) OriginalRequest() *http.Request { return r.RawRequest }
func (r *RequestValues) RawBody() []byte                { return r.RawBodyBytes }
func (r *RequestValues) ParsedBody() any                { return r.ParsedBodyData }
