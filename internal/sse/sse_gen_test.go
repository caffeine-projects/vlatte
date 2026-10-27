package sse

import (
	"time"

	"pgregory.net/rapid"
)

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

// _jsonData holds the JSON data of events: objects, arrays and scalars, and what a file writes as text: a string,
// null, and a number past int64.
var _jsonData = []string{`{"a":1,"b":[true,null,"x"]}`, `[]`, `{}`, `3`, `false`, `"s"`, `null`, `{"z":{"y":"é"}}`, `18446744073709551615`, `[1.5,-2]`}

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
			if ev.HasData && rapid.Bool().Draw(t, "json") {
				ev.Data, ev.DataKind = []byte(rapid.SampledFrom(_jsonData).Draw(t, "json_data")), DataJSON
			}
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
