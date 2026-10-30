package sse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestDoc_KeepsJSONNumbersAFileCantHold(t *testing.T) {
	// A number past int64, as a uint64 id, would round once a file reads it back, and one past float64 wouldn't read
	// back: each is written as its JSON text, which sends the same bytes.
	s := &Spec{Events: []*EventSpec{
		{Data: []byte(`18446744073709551615`), DataKind: DataJSON, HasData: true},
		{Data: []byte(`{"id":18446744073709551615}`), DataKind: DataJSON, HasData: true},
		{Data: []byte(`1e400`), DataKind: DataJSON, HasData: true},
	}}

	text, err := config.Encode(s.Doc(), "yaml")
	require.NoError(t, err)

	doc, err := config.Parse(text, "yaml")
	require.NoError(t, err)

	back, err := Decode(config.Value{Key: "event_stream", Any: doc})
	require.NoError(t, err)
	require.Equal(t, wire(t, s), wire(t, back), "%s", text)
}

func TestDoc_KeepsJSONStringsOfATemplatedStreamLiteral(t *testing.T) {
	// JSON data is never a template. A JSON string a file would read as text, a template in a templated stream, is
	// written as data_json: the stream, built back, sends it as it is.
	s := &Spec{Templated: true, Events: []*EventSpec{
		{Data: []byte(`"{{ .X }}"`), DataKind: DataJSON, HasData: true},
		{Data: []byte(`"{{ unbalanced"`), DataKind: DataJSON, HasData: true},
		{Data: []byte(`null`), DataKind: DataJSON, HasData: true},
	}}

	text, err := config.Encode(s.Doc(), "yaml")
	require.NoError(t, err)

	doc, err := config.Parse(text, "yaml")
	require.NoError(t, err)

	back, err := Decode(config.Value{Key: "event_stream", Any: doc})
	require.NoError(t, err)
	back.Templated = true

	data := map[string]any{"X": "rendered"}
	require.Equal(t, encode(t, s, data), encode(t, back, data), "%s", text)
}

func TestDoc_KeepsTheJSONOfATemplatedStreamOutOfItsTemplates(t *testing.T) {
	// The text of a templated stream is templates, and its JSON data isn't. JSON a file can't write back as the same
	// bytes, as a Go struct's with its keys in field order, is written as the JSON it holds: built back, the stream
	// sends it with its keys sorted, and never runs it. Only JSON with a number that would round is written as text,
	// which keeps the number's digits.
	tests := []struct {
		name string
		data string
		want string
	}{
		{"keys out of order", `{"b":"{{ .X }}","a":1}`, `{"a":1,"b":"{{ .X }}"}`},
		{"white space", `[ "{{ .X }}" ]`, `["{{ .X }}"]`},
		{"a number written another way", `{"n":1.0,"t":"{{ .X }}"}`, `{"n":1,"t":"{{ .X }}"}`},
		{"a number that would round", `{"id":18446744073709551615,"b":"x"}`, `{"id":18446744073709551615,"b":"x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Spec{Templated: true, Events: []*EventSpec{{Data: []byte(tt.data), DataKind: DataJSON, HasData: true}}}

			text, err := config.Encode(s.Doc(), "yaml")
			require.NoError(t, err)

			doc, err := config.Parse(text, "yaml")
			require.NoError(t, err)

			back, err := Decode(config.Value{Key: "event_stream", Any: doc})
			require.NoError(t, err)
			back.Templated = true

			require.Equal(t, "data: "+tt.want+"\n\n", encode(t, back, map[string]any{"X": "rendered"}), "%s", text)
		})
	}
}

// wire compiles s and returns its events as they go on the wire, rendered with an X when s is templated.
func wire(t require.TestingT, s *Spec) []Frame {
	e, err := Compile(s, _engine)
	require.NoError(t, err)

	frames, err := e.Encode(map[string]any{"X": "rendered"})
	require.NoError(t, err)

	return frames
}

func TestPropDoc_DecodesBackToTheSameStream(t *testing.T) {
	// SSE2: a stream written as a mock file, as a description writes it, reads back as a stream that sends the same
	// bytes, ids and delays, and keeps its settings. A templated stream's JSON data stays out of its templates.
	rapid.Check(t, func(t *rapid.T) {
		s := &Spec{
			Templated: rapid.Bool().Draw(t, "templated"),
			Events:    rapid.SliceOfN(genDocEvent(), 0, 5).Draw(t, "events"),
			KeepOpen:  rapid.Bool().Draw(t, "keep_open"),
			Resume:    rapid.Bool().Draw(t, "resume"),
			Heartbeat: rapid.SampledFrom([]time.Duration{0, time.Millisecond, 1500 * time.Millisecond}).Draw(t, "heartbeat"),
		}

		if s.HasRetry = rapid.Bool().Draw(t, "has_retry"); s.HasRetry {
			s.Retry = time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "retry"))
		}

		if rapid.Bool().Draw(t, "has_event_delay") {
			s.EventDelay = vlatte.Latency(time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "event_delay")))
		}

		text, err := config.Encode(s.Doc(), "yaml")
		require.NoError(t, err)

		doc, err := config.Parse(text, "yaml")
		require.NoError(t, err)

		back, err := Decode(config.Value{Key: "event_stream", Any: doc})
		require.NoError(t, err, "%s", text)

		// template.enabled sits beside event_stream, in the reply, which sets it.
		back.Templated = s.Templated

		require.Equal(t, wire(t, s), wire(t, back), "%s", text)
		require.Equal(t,
			[]any{s.HasRetry, s.Retry, s.EventDelay, s.Heartbeat, s.KeepOpen, s.Resume},
			[]any{back.HasRetry, back.Retry, back.EventDelay, back.Heartbeat, back.KeepOpen, back.Resume})
	})
}
