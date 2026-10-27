package sse

import (
	"cmp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

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
