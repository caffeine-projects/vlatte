package handler

import (
	json "encoding/json/v2"
	"net/http"
	"time"
)

// Types of the events of a mock server.
const (
	// EventMatched is a request a mock answered.
	EventMatched = "matched"
	// EventUnmatched is a request no mock matched.
	EventUnmatched = "unmatched"
	// EventProxied is a request the server proxied.
	EventProxied = "proxied"
	// EventWarning is something that went wrong, but didn't fail the request, such as a webhook that failed.
	EventWarning = "warning"
	// EventError is a request that failed.
	EventError = "error"
)

// Event is something that happened on a mock server, as GET /events streams it.
type Event struct {
	Type      string    `json:"type"`
	Time      time.Time `json:"time"`
	RequestID string    `json:"request_id,omitempty"`
	Method    string    `json:"method,omitempty"`
	Target    string    `json:"target,omitempty"`
	MockID    string    `json:"mock_id,omitempty"`
	Status    int       `json:"status,omitzero"`
	Message   string    `json:"message,omitempty"`
}

// Events are the events of a mock server, which it publishes and GET /events streams. It keeps none: they are no
// journal of requests.
type Events struct {
	stream *stream
}

// NewEvents creates the events of a mock server.
func NewEvents() *Events {
	return &Events{stream: newStream()}
}

// Publish sends e to the clients of GET /events. It never waits for them.
func (e *Events) Publish(event Event) {
	if !e.stream.listening() {
		return
	}

	data, err := json.Marshal(event, json.Deterministic(true))
	if err != nil {
		return
	}

	e.stream.publish(event.Type, data)
}

// Close ends the streams of GET /events.
func (e *Events) Close() {
	e.stream.close()
}

// EventStream streams the events of the mock server, as they happen, as Server-Sent Events: GET /events. Each event
// is a JSON Event, of the event type its type names.
func EventStream(events *Events) http.Handler {
	return http.HandlerFunc(events.stream.serve)
}
