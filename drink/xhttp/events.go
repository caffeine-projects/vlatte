package xhttp

import (
	"net/http"
	"net/url"
	"time"
)

// RequestValues groups HTTP request data, including the parsed body.
// It is used by several components during the request matching phase.
type RequestValues struct {
	// StartedAt indicates when the request arrived.
	StartedAt time.Time

	// RawRequest is the original incoming http.Request.
	RawRequest *http.Request

	// URL is the full request url.URL, including scheme, host, port.
	URL *url.URL

	// URLPathSegments stores path segments.
	// Eg.: /test/100 -> []string{"test", "100"}
	URLPathSegments []string

	// RawBody is the HTTP request body bytes.
	RawBody []byte

	// ParsedBody is the parsed http.Request body parsed by a RequestBodyParser instance.
	// It'll be nil if the HTTP request does not contain a body.
	ParsedBody any

	// App exposes application instance associated with the incoming HTTP request.
	App *HTTPMockApp

	// Mock is the matched Mock for the current HTTP request.
	Mock *HTTPMock

	Data any
}
