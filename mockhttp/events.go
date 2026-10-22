package mockhttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/caffeine-projects/vlatte"
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

	// URL is the full request url.URL, including scheme, host, port.
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

	// nearMisses are the mocks closest to matching a request no mock matched, when the server reports them.
	nearMisses []vlatte.NearMiss

	// journal is the entry of the request in the server's journal, nil when it keeps none.
	journal *journalCall

	// answer is the writer of the response, which keeps the status the client gets. A request the server doesn't
	// serve, such as one rebuilt from the journal, has none, and reaches no lifecycle.
	answer *answerWriter
}

// Accessors for the fields above.
func (r *RequestValues) ID() string                     { return r.ReqID }
func (r *RequestValues) StartedAt() time.Time           { return r.StartedAtTime }
func (r *RequestValues) OriginalRequest() *http.Request { return r.RawRequest }
func (r *RequestValues) RawBody() []byte                { return r.RawBodyBytes }
func (r *RequestValues) ParsedBody() any                { return r.ParsedBodyData }

// sent returns the status the server sent the client, 0 before it sends one.
func (r *RequestValues) sent() int {
	return r.answer.status
}

// panicked returns the status the client of a request whose serving panicked gets: the recovery middleware answers
// once the handler is done, with the status of failures, unless the reply sent one first, or the connection was taken
// over.
func (r *RequestValues) panicked() int {
	if r.sent() != 0 || r.answer.hijacked {
		return r.sent()
	}

	return r.App.config.ErrorStatusCode
}

// answered returns the status the client got, once a mock or a proxy answered it with res: the one sent, or res's,
// which vlatte writes itself on a connection it takes over, such as a CONNECT's or an upgrade's. A reply that wrote
// nothing, such as a Handler, answers 200, as net/http does.
func (r *RequestValues) answered(res *Response) int {
	switch {
	case r.sent() != 0:
		return r.sent()
	case res != nil:
		return res.StatusCode
	case r.answer.hijacked:
		return 0
	}

	return http.StatusOK
}
