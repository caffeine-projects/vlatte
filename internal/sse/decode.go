package sse

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"math/big"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

type (
	specField  = config.Field[Spec]
	eventField = config.Field[EventSpec]
)

// SpecSchema decodes the event_stream object of a mock file, as response.event_stream writes it. EventSchema decodes
// each of its events.
var SpecSchema = config.NewSchema(
	specField{
		Key: "events", Kind: config.Any, Usage: "Events of the stream, in order.",
		Set: func(s *Spec, v config.Value) error {
			// An event that fails to decode keeps its place, so the problems name the events as the file does.
			return config.Items(v, func(item config.Value) error {
				ev, err := decodeEvent(item)
				s.Events = append(s.Events, ev)

				return err
			})
		},
	},
	specField{
		Key: "retry", Kind: config.Duration,
		Usage: "Reconnection time sent first, which clients read in whole milliseconds: milliseconds or a Go duration (1.5s).",
		Set:   func(s *Spec, v config.Value) error { s.Retry, s.HasRetry = v.Dur, true; return nil },
	},
	specField{
		Key: "event_delay", Kind: config.Duration,
		Usage: "How long to wait before each event without a delay of its own: milliseconds or a Go duration (1.5s).",
		Set: func(s *Spec, v config.Value) error {
			s.EventDelay, s.FixedEventDelay, s.HasFixedEventDelay = vlatte.Latency(v.Dur), v.Dur, true
			return nil
		},
	},
	specField{
		Key: "heartbeat", Kind: config.Duration,
		Usage: "Send a comment each time the stream waits this long without sending anything: milliseconds or a Go duration (1.5s).",
		Set:   func(s *Spec, v config.Value) error { s.Heartbeat = v.Dur; return nil },
	},
	specField{
		Key: "keep_open", Kind: config.Bool, Usage: "Keep the stream open after its last event, until the client leaves.",
		Set: func(s *Spec, v config.Value) error { s.KeepOpen = v.Bool; return nil },
	},
	specField{
		Key: "resume", Kind: config.Bool, Usage: "Send a request with a Last-Event-ID only the events after the one with that id.",
		Set: func(s *Spec, v config.Value) error { s.Resume = v.Bool; return nil },
	},
)

// EventSchema decodes an event of an event_stream object.
var EventSchema = config.NewSchema(
	eventField{
		Key: "event", Kind: config.String, Usage: "Event type. Clients dispatch an event without one as a message.",
		Set: func(e *EventSpec, v config.Value) error { e.Event = v.Str; return nil },
	},
	eventField{
		Key: "id", Kind: config.String, Usage: "Event id, which clients send back as Last-Event-ID. An empty id resets it.",
		Set: func(e *EventSpec, v config.Value) error { e.ID, e.HasID = v.Str, true; return nil },
	},
	eventField{
		Key: "data", Kind: config.Any, Usage: "Data of the event: text as is, anything else as JSON. A data line per line.",
		Set: func(e *EventSpec, v config.Value) error {
			switch x := v.Any.(type) {
			case nil:
				return config.Invalid(v, v.Key+" is null", `remove it, write "" for empty data, or data_json: null for JSON null`)
			case string:
				e.Data, e.DataKind = []byte(x), DataText
			default:
				b, err := json.Marshal(x, json.Deterministic(true))
				if err != nil {
					return err
				}

				e.Data, e.DataKind = b, DataJSON
			}

			e.HasData = true

			return nil
		},
	},
	eventField{
		Key: "data_json", Kind: config.Any,
		Usage: "Data of the event as JSON, strings and null too, never a template. In place of data.",
		Set: func(e *EventSpec, v config.Value) error {
			b, err := json.Marshal(v.Any, json.Deterministic(true))
			if err != nil {
				return err
			}

			e.Data, e.DataKind, e.HasData = b, DataJSON, true

			return nil
		},
	},
	eventField{
		Key: "retry", Kind: config.Duration,
		Usage: "Reconnection time from this event on: milliseconds or a Go duration (1.5s).",
		Set:   func(e *EventSpec, v config.Value) error { e.Retry, e.HasRetry = v.Dur, true; return nil },
	},
	eventField{
		Key: "comment", Kind: config.String, Usage: "Comment sent first, a comment line per line, which clients skip.",
		Set: func(e *EventSpec, v config.Value) error { e.Comment = v.Str; return nil },
	},
	eventField{
		Key: "delay", Kind: config.Duration,
		Usage: "How long to wait before the event, in place of event_delay: milliseconds or a Go duration (1.5s).",
		Set:   func(e *EventSpec, v config.Value) error { e.Delay, e.HasDelay = v.Dur, true; return nil },
	},
	eventField{
		Key: "raw", Kind: config.String, Usage: "Text sent as it is, in place of the other fields, such as CRLF line endings.",
		Set: func(e *EventSpec, v config.Value) error { e.Raw, e.HasRaw = []byte(v.Str), true; return nil },
	},
)

// Decode decodes the event_stream object v of a mock file. Every problem is keyed under v.Key, as
// response.event_stream.events[1].raw. With problems, the spec is what decoded; it is nil when v isn't an object.
// Text that is a template, once the reply makes the stream templated, is checked by Compile.
func Decode(v config.Value) (*Spec, error) {
	s := new(Spec)

	err := SpecSchema.Decode(s, v)
	if _, ok := v.Any.(map[string]any); !ok {
		return nil, err
	}

	return s, err
}

// decodeEvent decodes an event. It returns the event even when it has problems.
func decodeEvent(v config.Value) (*EventSpec, error) {
	e := new(EventSpec)
	err := EventSchema.Decode(e, v)

	obj, _ := v.Any.(map[string]any)
	_, hasData := obj["data"]
	_, hasJSON := obj["data_json"]
	_, hasRaw := obj["raw"]
	_, hasDelay := obj["delay"]

	var conflicts []config.Problem
	if hasData && hasJSON {
		conflicts = append(conflicts, config.Problem{
			Code: config.CodeConflict, Origin: v.Origin, Key: v.Key + ".data_json",
			Message: v.Key + ".data_json is set with " + v.Key + ".data", Hint: "keep one of them",
		})
	}

	// A raw event waits its delay too: only the fields conflict with it.
	if hasRaw && len(obj) > 1 && (len(obj) != 2 || !hasDelay) {
		conflicts = append(conflicts, config.Problem{
			Code: config.CodeConflict, Origin: v.Origin, Key: v.Key + ".raw",
			Message: v.Key + ".raw is set with other fields", Hint: "raw is sent alone, as it is, with a delay at most: keep raw, or the fields",
		})
	}

	if len(conflicts) == 0 {
		return e, err
	}

	return e, &config.Error{Problems: append(config.ProblemsOf(v, err), conflicts...)}
}

// Doc returns s as a mock file writes it, under response.event_stream. An EventDelay that isn't fixed can't be
// written, and is left out.
func (s *Spec) Doc() map[string]any {
	events := make([]any, len(s.Events))
	for i, e := range s.Events {
		events[i] = e.doc(s.Templated)
	}

	doc := map[string]any{"events": events}

	durations := []struct {
		key string
		d   time.Duration
		set bool
	}{
		{"retry", s.Retry, s.HasRetry},
		{"event_delay", s.FixedEventDelay, s.HasFixedEventDelay},
		{"heartbeat", s.Heartbeat, s.Heartbeat > 0},
	}
	for _, f := range durations {
		if f.set {
			doc[f.key] = f.d.String()
		}
	}

	if s.KeepOpen {
		doc["keep_open"] = true
	}

	if s.Resume {
		doc["resume"] = true
	}

	return doc
}

// doc returns the event as a mock file writes it, in a stream that is templated or not.
func (e *EventSpec) doc(templated bool) map[string]any {
	doc := make(map[string]any)

	if e.HasDelay {
		doc["delay"] = e.Delay.String()
	}

	if e.HasRaw {
		doc["raw"] = string(e.Raw)
		return doc
	}

	if e.Event != "" {
		doc["event"] = e.Event
	}

	if e.HasID {
		doc["id"] = e.ID
	}

	if e.Comment != "" {
		doc["comment"] = e.Comment
	}

	if e.HasRetry {
		doc["retry"] = e.Retry.String()
	}

	if e.HasData {
		key, data := e.dataDoc(templated)
		doc[key] = data
	}

	return doc
}

// dataDoc returns the data as a mock file writes it, and its key: text as text, and JSON as the value it holds, under
// data, or data_json for a string or null, which data reads as text or refuses. JSON its value doesn't write back as
// the same bytes, as a Go struct's with its keys in field order, is written as its text, which sends them. In a
// templated stream that text would be a template, so there only JSON with a number that would round, as one past
// int64, is text: other JSON is written as its value, and sent as encoding/json writes it, compact, with its object
// keys sorted.
func (e *EventSpec) dataDoc(templated bool) (string, any) {
	if e.DataKind != DataJSON {
		return "data", string(e.Data)
	}

	v, err := value.DecodeJSON(e.Data)
	if err != nil {
		return "data", string(e.Data)
	}

	if b, _ := json.Marshal(v, json.Deterministic(true)); !bytes.Equal(b, e.Data) && (!templated || !numbersHold(e.Data)) {
		return "data", string(e.Data)
	}

	switch v.(type) {
	case string, nil:
		return "data_json", v
	}

	return "data", v
}

// numbersHold reports whether each number of data, JSON, decodes to a value written back as the same number, as 1.0
// is written back as 1. One past int64 rounds.
func numbersHold(data []byte) bool {
	dec := jsontext.NewDecoder(bytes.NewReader(data))

	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return errors.Is(err, io.EOF)
		}

		if tok.Kind() != jsontext.KindNumber {
			continue
		}

		n, _ := value.DecodeJSON([]byte(tok.String()))
		if b, _ := json.Marshal(n); !sameNumber(tok.String(), string(b)) {
			return false
		}
	}
}

// sameNumber reports whether a and b, JSON numbers, are the same number. They are compared at 512 bits, far past
// the 53 of a float64: a number that rounds differs from what it rounds to, and 1.0 is 1.
func sameNumber(a, b string) bool {
	x, _, errA := big.ParseFloat(a, 10, 512, big.ToNearestEven)
	y, _, errB := big.ParseFloat(b, 10, 512, big.ToNearestEven)

	return errA == nil && errB == nil && x.Cmp(y) == 0
}
