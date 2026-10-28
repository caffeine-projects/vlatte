package mockhttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
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

	// URL is the full request url.URL, including scheme, host, port: those a request in absolute form, as a forward
	// proxy gets, or an intercepted one names, and the server's otherwise.
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

	// scenarioMoves are the moves of the scenarios of the mock Find picked.
	scenarioMoves []matchscenario.Move

	// journal is the entry of the request in the server's journal, nil when it keeps none.
	journal *journalCall

	// answer keeps the status the client gets, and holds the outcomes of the request until the client has its
	// answer: see answerWriter. A request the server doesn't serve, such as one rebuilt from the journal, has none, and
	// reaches no lifecycle.
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
	status, _, _ := r.answer.state()
	return status
}

// panicked returns the status the client of a request whose serving panicked gets. The writer outside the
// middlewares has the answer of the one that recovered the panic, by the time the outcomes run. A handler's own writer
// doesn't: the recovery middleware answers once the handler is done, with the status of failures, unless the reply
// sent one first, or the connection was taken over.
func (r *RequestValues) panicked() int {
	if r.answer.outer {
		return r.answered()
	}

	if status, hijacked, _ := r.answer.state(); status != 0 || hijacked {
		return status
	}

	return r.App.config.ErrorStatusCode
}

// answered returns the status the client got: the one sent, or the one vlatte wrote on a connection it took over,
// such as a CONNECT's or an upgrade's. A handler that wrote nothing, such as a Handler reply, answers 200, as net/http
// does, unless the connection was taken over, or dropped by a panic.
func (r *RequestValues) answered() int {
	status, hijacked, unwinding := r.answer.state()

	switch {
	case status != 0:
		return status
	case hijacked || unwinding:
		return 0
	}

	return http.StatusOK
}
