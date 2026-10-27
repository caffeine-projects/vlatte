package sse

import (
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
