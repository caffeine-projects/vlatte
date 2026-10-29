package mockhttp

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/sse"
)

var (
	_ io.ReadCloser = (*eventStreamBody)(nil)
	_ keptBody      = (*eventStreamBody)(nil)
)

// EventStream is a body of Server-Sent Events, for a reply: the text/event-stream body of the WHATWG HTML standard,
// which the reply sends event by event, each flushed to the client as it goes. Start one with [NewEventStream], and
// set it with [Reply.EventStream].
//
// The stream ends after its last event, unless it keeps open: see [EventStream.KeepOpen]. The mock counts the request
// once the stream starts, so [Builder.Once] serves one client and a scenario moves on while it is open. It runs its
// callbacks and on_complete events once the stream ends, with all it sent.
type EventStream struct {
	spec sse.Spec
}

// NewEventStream starts an event stream with events, sent in order. The events are copied: changing one afterwards
// doesn't change the stream.
func NewEventStream(events ...*Event) *EventStream {
	es := &EventStream{spec: sse.Spec{Events: make([]*sse.EventSpec, len(events))}}
	for i, ev := range events {
		spec := ev.spec
		spec.Data = bytes.Clone(ev.spec.Data)
		spec.Raw = bytes.Clone(ev.spec.Raw)
		es.spec.Events[i] = &spec
	}

	return es
}

// Retry sends d first, as the retry field: how long a client waits before it reconnects once the stream ends. Clients
// read it in whole milliseconds.
func (es *EventStream) Retry(d time.Duration) *EventStream {
	es.spec.Retry, es.spec.HasRetry = d, true
	return es
}

// EventDelay waits d before each event that has no delay of its own: a stream that trickles in. A negative d waits for
// none.
func (es *EventStream) EventDelay(d time.Duration) *EventStream {
	es.spec.EventDelay = vlatte.Latency(d)
	es.spec.FixedEventDelay, es.spec.HasFixedEventDelay = max(d, 0), true

	return es
}

// EventDelayFunc sets a function that picks the wait before each event that has no delay of its own, such as
// [vlatte.Jitter]. A mock file can't write a function: [Reply.Describe] leaves it out.
func (es *EventStream) EventDelayFunc(delay vlatte.Delay) *EventStream {
	es.spec.EventDelay = delay
	es.spec.FixedEventDelay, es.spec.HasFixedEventDelay = 0, false

	return es
}

// Heartbeat sends a comment, which clients skip, each time the stream waits d without sending anything: while it
// waits for an event, and while it keeps open. Proxies and clients that drop idle connections keep it.
func (es *EventStream) Heartbeat(d time.Duration) *EventStream {
	es.spec.Heartbeat = d
	return es
}

// KeepOpen keeps the stream open after its last event, until the client leaves or the server closes.
func (es *EventStream) KeepOpen() *EventStream {
	es.spec.KeepOpen = true
	return es
}

// ResumeFromLastEventID makes a request with a Last-Event-ID header, as a client sends when it reconnects, get only
// the events after the one with that id. A request with an id no event has gets them all. The retry field is sent
// either way.
func (es *EventStream) ResumeFromLastEventID() *EventStream {
	es.spec.Resume = true
	return es
}

// Template makes the text of the events templates, compiled with the template engine of the server and rendered with
// the data of each request: their types, ids, comments and text data. JSON data and raw events are sent as they are.
// The mock's description keeps JSON data as JSON: built back, the stream sends it compact, with its object keys
// sorted. JSON with a number past int64, which a file can't hold, is written as its text, a template then.
func (es *EventStream) Template() *EventStream {
	es.spec.Templated = true
	return es
}

// Event is an event of an event stream. Start one with [NewEvent].
//
// Its fields go on the wire as the event stream format reads them: data with line breaks becomes a data line per
// line, so clients read it back whole. An event type or id with a line break, or an id with a NUL, makes building the
// mock fail, or, in a templated stream, the reply that renders it. An event without data isn't dispatched by
// clients, though its id still counts.
type Event struct {
	spec sse.EventSpec
}

// NewEvent starts an event.
func NewEvent() *Event {
	return &Event{}
}

// Name sets the event type, the event field. Clients dispatch an event without one as a message.
func (ev *Event) Name(name string) *Event {
	ev.spec.Event = name
	return ev
}

// ID sets the id of the event, which clients send back as Last-Event-ID when they reconnect. An empty id is sent too:
// it resets the client's last event ID.
func (ev *Event) ID(id string) *Event {
	ev.spec.ID, ev.spec.HasID = id, true
	return ev
}

// Data sets the data of the event, a template when the stream is templated. Like every data setter, it replaces the
// data an earlier one set.
func (ev *Event) Data(text string) *Event {
	ev.spec.Data, ev.spec.DataKind, ev.spec.HasData = []byte(text), sse.DataText, true
	ev.spec.Err = nil

	return ev
}

// DataJSON sets the data of the event to the JSON of v. A value that doesn't marshal makes building the mock fail.
func (ev *Event) DataJSON(v any) *Event {
	b, err := json.Marshal(v, json.Deterministic(true))
	ev.spec.Data, ev.spec.DataKind, ev.spec.HasData = b, sse.DataJSON, true
	ev.spec.Err = err

	return ev
}

// Retry sets the retry field of the event: how long the client waits before it reconnects from now on.
func (ev *Event) Retry(d time.Duration) *Event {
	ev.spec.Retry, ev.spec.HasRetry = d, true
	return ev
}

// Comment sets the comment of the event, sent first, a comment line per line, which clients skip. An event with only
// a comment is a comment.
func (ev *Event) Comment(text string) *Event {
	ev.spec.Comment = text
	return ev
}

// Delay waits d before the event, in place of the stream's EventDelay: the events before it are sent and flushed
// first, so the client reads them while it waits.
func (ev *Event) Delay(d time.Duration) *Event {
	ev.spec.Delay, ev.spec.HasDelay = d, true
	return ev
}

// Raw sends text as it is in place of the event's fields, which it can't be set with, to test what the fields can't
// write: CRLF or CR line endings, a byte order mark, an event without the blank line that ends it, unknown fields.
// An id line in the text is sent but has no id for [EventStream.ResumeFromLastEventID], which reads only [Event.ID].
// A byte order mark counts only as the first bytes of the stream: not after the stream's retry, set with
// [EventStream.Retry], or a heartbeat.
func (ev *Event) Raw(text string) *Event {
	ev.spec.Raw, ev.spec.HasRaw = []byte(text), true
	return ev
}

// eventStreamBody is the body of a reply with an event stream: it streams to the client, and, with keep, keeps what
// it sent, up to _keptBodyLimit, for what reads the body once it is sent.
type eventStreamBody struct {
	keeper

	stream *sse.Stream
}

// Read reads the next event, or heartbeat, once it is due, and keeps it, with keep.
func (b *eventStreamBody) Read(p []byte) (int, error) {
	n, err := b.stream.Read(p)
	b.record(p[:n], err)

	return n, err
}

// Close ends the stream.
func (b *eventStreamBody) Close() error {
	return b.stream.Close()
}
