// Package sse writes Server-Sent Events, the text/event-stream bodies of the WHATWG HTML standard, for replies: their
// model, the checks of the model, and the stream that sends them one by one, with their delays.
package sse

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
)

// _heartbeat is the comment a stream sends while it waits longer than its heartbeat: clients skip it.
var _heartbeat = []byte(": heartbeat\n")

// Spec is an event stream to send.
type Spec struct {
	Events     []*EventSpec
	Retry      time.Duration // the reconnection time sent first, when HasRetry is set
	HasRetry   bool
	EventDelay vlatte.Delay  // picks the wait before each event that has no delay of its own; nil waits for none
	Heartbeat  time.Duration // sends a comment after this long without a write; 0 sends none
	KeepOpen   bool          // the stream stays open after the last event, until the client leaves
	Resume     bool          // a request with a Last-Event-ID gets the events after the one with that id
	Templated  bool          // the text of the events is templates: see Compile
}

// DataKind says what the data of an event is.
type DataKind int

const (
	DataText DataKind = iota // text, a template when the spec is templated
	DataJSON                 // JSON, written as it is
)

// EventSpec is an event of a stream: its fields, or Raw bytes in their place.
type EventSpec struct {
	Event    string // the event type
	ID       string
	HasID    bool // an empty ID is sent too: it resets the last event ID of the client
	Data     []byte
	DataKind DataKind
	HasData  bool
	Retry    time.Duration
	HasRetry bool
	Comment  string
	Raw      []byte        // sent as it is, for what the fields can't write, such as CRLF line endings
	HasRaw   bool          // Raw excludes the fields
	Delay    time.Duration // the wait before the event, in place of the stream's EventDelay
	HasDelay bool
	Err      error // what went wrong building the event, such as JSON data that doesn't marshal
}

// Check returns what makes s a stream that can't be sent as asked, all of it joined, or nil.
func (s *Spec) Check() error {
	var errs []error

	if s.HasRetry && s.Retry < 0 {
		errs = append(errs, fmt.Errorf("retry must not be negative, not %s", s.Retry))
	}

	if s.Heartbeat < 0 {
		errs = append(errs, fmt.Errorf("heartbeat must not be negative, not %s", s.Heartbeat))
	}

	for i, e := range s.Events {
		if err := e.check(); err != nil {
			errs = append(errs, fmt.Errorf("events[%d]: %w", i, err))
		}
	}

	return errors.Join(errs...)
}

// check returns what makes e an event that can't be sent as asked. The text of a templated event is checked once
// rendered.
func (e *EventSpec) check() error {
	var errs []error

	if e.Err != nil {
		errs = append(errs, e.Err)
	}

	if e.HasRaw && (e.Event != "" || e.HasID || e.HasData || e.HasRetry || e.Comment != "") {
		errs = append(errs, errors.New("raw is set with other fields: raw is sent alone, as it is"))
	}

	if e.HasRetry && e.Retry < 0 {
		errs = append(errs, fmt.Errorf("retry must not be negative, not %s", e.Retry))
	}

	if e.HasDelay && e.Delay < 0 {
		errs = append(errs, fmt.Errorf("delay must not be negative, not %s", e.Delay))
	}

	return errors.Join(errs...)
}

// Encoder renders the events of a Spec. It is safe for concurrent use.
type Encoder struct {
	head       []byte // the retry field the stream starts with, if any
	events     []compiledEvent
	eventDelay vlatte.Delay
	heartbeat  time.Duration
	keepOpen   bool
	resume     bool
}

// compiledEvent is an event with its templates parsed.
type compiledEvent struct {
	event    text
	id       text
	hasID    bool
	data     text
	hasData  bool
	retry    time.Duration
	hasRetry bool
	comment  text
	raw      []byte
	hasRaw   bool
	delay    time.Duration
	hasDelay bool
}

// text is a text of an event: a template, or the text itself.
type text struct {
	raw  string
	tmpl vlatte.Template
}

// Compile checks s and compiles its templates with engine. The Encoder copies s: changing s afterwards doesn't change
// it.
//
// When s is templated, the event types, ids, comments and text data are templates. JSON data and raw events are sent
// as they are. engine can be nil when s isn't templated.
func Compile(s *Spec, engine vlatte.TemplateEngine) (*Encoder, error) {
	if s.Templated && engine == nil {
		return nil, errors.New("sse: a templated stream needs a template engine")
	}

	if err := s.Check(); err != nil {
		return nil, fmt.Errorf("sse: %w", err)
	}

	e := &Encoder{
		events:     make([]compiledEvent, len(s.Events)),
		eventDelay: s.EventDelay,
		heartbeat:  s.Heartbeat,
		keepOpen:   s.KeepOpen,
		resume:     s.Resume,
	}

	if s.HasRetry {
		e.head = appendRetry(nil, s.Retry)
		e.head = append(e.head, '\n')
	}

	if !s.Templated {
		engine = nil
	}

	for i, spec := range s.Events {
		ev, err := compileEvent(spec, engine)
		if err != nil {
			return nil, fmt.Errorf("sse: events[%d]: %w", i, err)
		}

		// Text that isn't a template is checked now, so the mock fails to build rather than its replies.
		if engine == nil {
			if _, err := ev.render(nil); err != nil {
				return nil, fmt.Errorf("sse: events[%d]: %w", i, err)
			}
		}

		e.events[i] = ev
	}

	return e, nil
}

// compileEvent compiles the texts of spec with engine, or keeps them as text when engine is nil.
func compileEvent(spec *EventSpec, engine vlatte.TemplateEngine) (compiledEvent, error) {
	var err error

	parse := func(field, s string) text {
		t := text{raw: s}
		if engine != nil && err == nil {
			if t.tmpl, err = engine.Compile(field, s); err != nil {
				err = fmt.Errorf("%s: %w", field, err)
			}
		}

		return t
	}

	ev := compiledEvent{
		event:    parse("event", spec.Event),
		id:       parse("id", spec.ID),
		hasID:    spec.HasID,
		hasData:  spec.HasData,
		retry:    spec.Retry,
		hasRetry: spec.HasRetry,
		comment:  parse("comment", spec.Comment),
		raw:      bytes.Clone(spec.Raw),
		hasRaw:   spec.HasRaw,
		delay:    spec.Delay,
		hasDelay: spec.HasDelay,
	}

	if spec.DataKind == DataText {
		ev.data = parse("data", string(spec.Data))
	} else {
		ev.data = text{raw: string(spec.Data)}
	}

	return ev, err
}

// Frame is an event as it goes on the wire, with the wait before it.
type Frame struct {
	Bytes    []byte
	ID       string // the id of the event, when HasID is set
	HasID    bool
	Delay    time.Duration // the wait before the event, when HasDelay is set
	HasDelay bool
}

// Encode renders the events with data for their templates.
func (e *Encoder) Encode(data any) ([]Frame, error) {
	frames := make([]Frame, len(e.events))
	for i := range e.events {
		f, err := e.events[i].render(data)
		if err != nil {
			return nil, fmt.Errorf("sse: events[%d]: %w", i, err)
		}

		frames[i] = f
	}

	return frames, nil
}

// render writes the event as the WHATWG event stream format reads it: a comment line per line of the comment, the
// event, id and retry fields, a data line per line of the data, then a blank line, which dispatches the event.
func (ev *compiledEvent) render(data any) (Frame, error) {
	f := Frame{HasID: ev.hasID, Delay: ev.delay, HasDelay: ev.hasDelay}

	if ev.hasRaw {
		f.Bytes = ev.raw
		return f, nil
	}

	comment, err := ev.comment.render(data)
	if err != nil {
		return Frame{}, fmt.Errorf("comment: %w", err)
	}

	event, err := ev.event.render(data)
	if err != nil {
		return Frame{}, fmt.Errorf("event: %w", err)
	}

	id, err := ev.id.render(data)
	if err != nil {
		return Frame{}, fmt.Errorf("id: %w", err)
	}

	value, err := ev.data.render(data)
	if err != nil {
		return Frame{}, fmt.Errorf("data: %w", err)
	}

	// A line break would end the field there, and what follows would read as another field.
	switch {
	case strings.ContainsAny(event, "\r\n"):
		return Frame{}, errors.New("event holds a line break")
	case strings.ContainsAny(id, "\r\n"):
		return Frame{}, errors.New("id holds a line break")
	case strings.ContainsRune(id, 0):
		// Clients ignore an id with a NUL.
		return Frame{}, errors.New("id holds a NUL character")
	}

	var b []byte

	if comment != "" {
		for _, line := range lines(comment) {
			b = append(b, ": "...)
			b = append(b, line...)
			b = append(b, '\n')
		}
	}

	if event != "" {
		b = appendField(b, "event", event)
	}

	if ev.hasID {
		b = appendField(b, "id", id)
		f.ID = id
	}

	if ev.hasRetry {
		b = appendRetry(b, ev.retry)
	}

	if ev.hasData {
		for _, line := range lines(value) {
			b = appendField(b, "data", line)
		}
	}

	f.Bytes = append(b, '\n')

	return f, nil
}

// render renders the text with data, or returns it as it is when it isn't a template.
func (t text) render(data any) (string, error) {
	if t.tmpl == nil {
		return t.raw, nil
	}

	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// lines returns the lines of s, split where the event stream format ends a line: at CRLF, CR or LF. A line break at
// the end makes an empty last line, so the client reads the break back.
func lines(s string) []string {
	var out []string

	for {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			return append(out, s)
		}

		out = append(out, s[:i])

		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			i++
		}

		s = s[i+1:]
	}
}

func appendField(b []byte, name, value string) []byte {
	b = append(b, name...)
	b = append(b, ": "...)
	b = append(b, value...)

	return append(b, '\n')
}

// appendRetry appends the retry field of d, in whole milliseconds, as clients read it.
func appendRetry(b []byte, d time.Duration) []byte {
	return appendField(b, "retry", strconv.FormatInt(d.Milliseconds(), 10))
}
