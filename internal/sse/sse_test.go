package sse

import (
	"cmp"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// _engine compiles the templates of the specs under test.
var _engine = vlatte.MustNewGoTemplateEngine()

// encode compiles s and renders its events with data, joined as they go on the wire.
func encode(t *testing.T, s *Spec, data any) string {
	t.Helper()

	e, err := Compile(s, _engine)
	require.NoError(t, err)

	frames, err := e.Encode(data)
	require.NoError(t, err)

	var b strings.Builder
	for _, f := range frames {
		b.Write(f.Bytes)
	}

	return b.String()
}

func TestEncode_WritesEachFieldAsTheStandardReadsIt(t *testing.T) {
	tests := []struct {
		name  string
		event *EventSpec
		want  string
	}{
		{
			name:  "data only",
			event: &EventSpec{Data: []byte("hello"), HasData: true},
			want:  "data: hello\n\n",
		},
		{
			name:  "every field, comment first",
			event: &EventSpec{Comment: "c", Event: "price", ID: "7", HasID: true, Retry: 1500 * time.Millisecond, HasRetry: true, Data: []byte("1"), HasData: true},
			want:  ": c\nevent: price\nid: 7\nretry: 1500\ndata: 1\n\n",
		},
		{
			// An empty id resets the last event ID of the client: it is sent.
			name:  "empty id",
			event: &EventSpec{ID: "", HasID: true, Data: []byte("x"), HasData: true},
			want:  "id: \ndata: x\n\n",
		},
		{
			// Empty data still dispatches an event, with "" as its data.
			name:  "empty data",
			event: &EventSpec{Data: nil, HasData: true},
			want:  "data: \n\n",
		},
		{
			// An event without data isn't dispatched, but its id still reaches the client: it is sent as asked.
			name:  "no data",
			event: &EventSpec{ID: "1", HasID: true},
			want:  "id: 1\n\n",
		},
		{
			name:  "raw, as it is",
			event: &EventSpec{Raw: []byte("data: a\r\n\r\n"), HasRaw: true},
			want:  "data: a\r\n\r\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, encode(t, &Spec{Events: []*EventSpec{tc.event}}, nil))
		})
	}
}

func TestEncode_SplitsDataAndCommentsAtEveryLineEnding(t *testing.T) {
	// CRLF, CR and LF all end a line in the event stream format: a data line per line keeps the data as it was.
	s := &Spec{Events: []*EventSpec{
		{Comment: "a\nb", Data: []byte("1\r\n2\r3\n4\n"), HasData: true},
	}}

	require.Equal(t, ": a\n: b\ndata: 1\ndata: 2\ndata: 3\ndata: 4\ndata: \n\n", encode(t, s, nil))
}

func TestEncode_RendersTemplatedTextButNotJSON(t *testing.T) {
	s := &Spec{Templated: true, Events: []*EventSpec{
		{Event: "{{ .E }}", ID: "{{ .ID }}", HasID: true, Comment: "{{ .C }}", Data: []byte("{{ .D }}"), HasData: true},
		{Data: []byte(`{"a":"{{ .D }}"}`), DataKind: DataJSON, HasData: true},
	}}

	got := encode(t, s, map[string]any{"E": "tick", "ID": "9", "C": "note", "D": "v"})
	require.Equal(t, ": note\nevent: tick\nid: 9\ndata: v\n\ndata: {\"a\":\"{{ .D }}\"}\n\n", got)
}

func TestCompile_RefusesWhatWouldBreakTheStream(t *testing.T) {
	errJSON := errors.New("json: cannot marshal")

	tests := []struct {
		name string
		spec *Spec
		want string
	}{
		{"line break in event", &Spec{Events: []*EventSpec{{Event: "a\nb"}}}, "sse: events[0]: event holds a line break"},
		{"line break in id", &Spec{Events: []*EventSpec{{ID: "a\rb", HasID: true}}}, "sse: events[0]: id holds a line break"},
		{"NUL in id", &Spec{Events: []*EventSpec{{ID: "a\x00", HasID: true}}}, "sse: events[0]: id holds a NUL character"},
		{"raw with fields", &Spec{Events: []*EventSpec{{Raw: []byte("x"), HasRaw: true, Event: "e"}}}, "sse: events[0]: raw is set with other fields: raw is sent alone, as it is"},
		{"negative event retry", &Spec{Events: []*EventSpec{{Retry: -time.Second, HasRetry: true}}}, "sse: events[0]: retry must not be negative, not -1s"},
		{"negative delay", &Spec{Events: []*EventSpec{{Delay: -time.Second, HasDelay: true}}}, "sse: events[0]: delay must not be negative, not -1s"},
		{"negative retry", &Spec{Retry: -time.Second, HasRetry: true}, "sse: retry must not be negative, not -1s"},
		{"negative heartbeat", &Spec{Heartbeat: -time.Second}, "sse: heartbeat must not be negative, not -1s"},
		{"event error", &Spec{Events: []*EventSpec{{Err: errJSON}}}, "sse: events[0]: json: cannot marshal"},
		{"template without engine", &Spec{Templated: true}, "sse: a templated stream needs a template engine"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var engine vlatte.TemplateEngine
			if !tc.spec.Templated {
				engine = _engine
			}

			_, err := Compile(tc.spec, engine)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestEncode_RefusesALineBreakATemplateRendered(t *testing.T) {
	e, err := Compile(&Spec{Templated: true, Events: []*EventSpec{{}, {ID: "{{ .ID }}", HasID: true}}}, _engine)
	require.NoError(t, err)

	_, err = e.Encode(map[string]any{"ID": "1\n2"})
	require.EqualError(t, err, "sse: events[1]: id holds a line break")
}

func TestCompile_RefusesATemplateThatDoesntCompile(t *testing.T) {
	_, err := Compile(&Spec{Templated: true, Events: []*EventSpec{{Data: []byte("{{ .X"), HasData: true}}}, _engine)
	require.ErrorContains(t, err, "sse: events[0]: data: ")
}

func TestEncode_NamesTheFieldWhoseTemplateFails(t *testing.T) {
	// A template that fails at a request names its event and field, so the failure answer points at the mock's text.
	fail := "{{ .X.Y }}" // X is text: it has no field Y

	tests := []struct {
		name  string
		event *EventSpec
		want  string
	}{
		{"comment", &EventSpec{Comment: fail}, "sse: events[0]: comment: "},
		{"event", &EventSpec{Event: fail}, "sse: events[0]: event: "},
		{"id", &EventSpec{ID: fail, HasID: true}, "sse: events[0]: id: "},
		{"data", &EventSpec{Data: []byte(fail), HasData: true}, "sse: events[0]: data: "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Compile(&Spec{Templated: true, Events: []*EventSpec{tc.event}}, _engine)
			require.NoError(t, err)

			_, err = e.Stream(t.Context(), map[string]any{"X": "text"}, "")
			require.ErrorContains(t, err, tc.want)
			require.ErrorContains(t, err, "can't evaluate field Y")
		})
	}
}

// dispatched is an event as a client dispatches it.
type dispatched struct {
	Type        string
	Data        string
	LastEventID string
}

// parse reads body as the WHATWG HTML standard's event stream interpretation does, and returns the events a client
// dispatches.
func parse(body string) []dispatched {
	var (
		events                  []dispatched
		data, eventType, lastID string
		hasData                 bool
	)

	body = strings.TrimPrefix(body, "\uFEFF")

	// Split apart from lines, the splitter under test.
	for line := range strings.SplitSeq(normalize(body), "\n") {
		switch {
		case line == "":
			if hasData {
				events = append(events, dispatched{Type: cmp.Or(eventType, "message"), Data: strings.TrimSuffix(data, "\n"), LastEventID: lastID})
			}

			data, eventType, hasData = "", "", false
		case strings.HasPrefix(line, ":"):
		default:
			name, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")

			switch name {
			case "event":
				eventType = value
			case "data":
				data += value + "\n"
				hasData = true
			case "id":
				if !strings.ContainsRune(value, 0) {
					lastID = value
				}
			}
		}
	}

	return events
}

func TestPropEncode_ClientsReadTheEventsBack(t *testing.T) {
	// SSE1: whatever the data, comments, types and ids, a client dispatches each event with data, in order, with the
	// type, data and last event ID it was written with. Events without data aren't dispatched, but their ids count.
	rapid.Check(t, func(t *rapid.T) {
		events := rapid.SliceOfN(genEvent(), 0, 6).Draw(t, "events")

		e, err := Compile(&Spec{Events: events}, nil)
		require.NoError(t, err)

		frames, err := e.Encode(nil)
		require.NoError(t, err)

		var body strings.Builder
		for _, f := range frames {
			body.Write(f.Bytes)
		}

		var (
			want   []dispatched
			lastID string
		)

		for _, ev := range events {
			if ev.HasID {
				lastID = ev.ID
			}

			if ev.HasData {
				want = append(want, dispatched{Type: cmp.Or(ev.Event, "message"), Data: normalize(string(ev.Data)), LastEventID: lastID})
			}
		}

		require.Equal(t, want, parse(body.String()))
	})
}

// normalize returns s with its line endings as a client reads them back: LF.
func normalize(s string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
}

// Generators

// genFieldText draws the text of an event type or id: anything but CR, LF and NUL, which Compile refuses, spaces
// at either end included, which the format keeps but for the one after the colon.
func genFieldText() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 :._\-éü😀]{0,8}`)
}

// genText draws data and comments: unicode, colons, spaces and every line ending, at the ends too.
func genText() *rapid.Generator[string] {
	return rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"\r\n", "\r", "\n", " ", ":", "a", "data: x", "é", "😀"}), 0, 10),
		func(chunks []string) string {
			var s string
			for _, c := range chunks {
				s += c
			}

			return s
		})
}

// genEvent draws an event with any of its fields, but raw.
func genEvent() *rapid.Generator[*EventSpec] {
	return rapid.Custom(func(t *rapid.T) *EventSpec {
		ev := &EventSpec{
			Event:   rapid.OneOf(rapid.Just(""), genFieldText()).Draw(t, "event"),
			HasID:   rapid.Bool().Draw(t, "has_id"),
			HasData: rapid.Bool().Draw(t, "has_data"),
			Comment: rapid.OneOf(rapid.Just(""), genText()).Draw(t, "comment"),
		}

		if ev.HasID {
			ev.ID = genFieldText().Draw(t, "id")
		}

		if ev.HasData {
			ev.Data = []byte(genText().Draw(t, "data"))
		}

		return ev
	})
}

// _jsonData holds the JSON data of events: objects, arrays and scalars, what data reads as text or refuses, a string
// and null, a number past int64, which a file can't hold, and strings a templated stream would run as templates.
var _jsonData = []string{
	`{"a":1,"b":[true,null,"x"]}`, `[]`, `{}`, `3`, `false`, `"s"`, `null`, `{"z":{"y":"é"}}`, `18446744073709551615`,
	`[1.5,-2]`, `"{{ .X }}"`, `{"a":"{{ .X }}"}`,
}

// genDocEvent draws an event as a mock file can write it: any field of genEvent, JSON data, a retry, or raw text
// alone, each with a delay or not.
func genDocEvent() *rapid.Generator[*EventSpec] {
	return rapid.Custom(func(t *rapid.T) *EventSpec {
		ev := new(EventSpec)

		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			ev.Raw, ev.HasRaw = []byte(genText().Draw(t, "raw")), true
		case 1:
			ev = genEvent().Draw(t, "event")
			ev.Data, ev.DataKind, ev.HasData = []byte(rapid.SampledFrom(_jsonData).Draw(t, "json_data")), DataJSON, true
		default:
			ev = genEvent().Draw(t, "event")
		}

		if !ev.HasRaw {
			if ev.HasRetry = rapid.Bool().Draw(t, "has_retry"); ev.HasRetry {
				ev.Retry = time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "retry"))
			}
		}

		if ev.HasDelay = rapid.Bool().Draw(t, "has_delay"); ev.HasDelay {
			ev.Delay = time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "delay"))
		}

		return ev
	})
}
