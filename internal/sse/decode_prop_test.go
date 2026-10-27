package sse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// wire compiles s and returns its events as they go on the wire.
func wire(t require.TestingT, s *Spec) []Frame {
	e, err := Compile(s, nil)
	require.NoError(t, err)

	frames, err := e.Encode(nil)
	require.NoError(t, err)

	return frames
}

func TestPropDoc_DecodesBackToTheSameStream(t *testing.T) {
	// SSE2: a stream written as a mock file, as a description writes it, reads back as a stream that sends the same
	// bytes, ids and delays, and keeps its settings.
	rapid.Check(t, func(t *rapid.T) {
		s := &Spec{
			Events:    rapid.SliceOfN(genDocEvent(), 0, 5).Draw(t, "events"),
			KeepOpen:  rapid.Bool().Draw(t, "keep_open"),
			Resume:    rapid.Bool().Draw(t, "resume"),
			Heartbeat: rapid.SampledFrom([]time.Duration{0, time.Millisecond, 1500 * time.Millisecond}).Draw(t, "heartbeat"),
		}

		if s.HasRetry = rapid.Bool().Draw(t, "has_retry"); s.HasRetry {
			s.Retry = time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "retry"))
		}

		if s.HasFixedEventDelay = rapid.Bool().Draw(t, "has_event_delay"); s.HasFixedEventDelay {
			s.FixedEventDelay = time.Duration(rapid.Int64Range(0, 1e12).Draw(t, "event_delay"))
			s.EventDelay = vlatte.Latency(s.FixedEventDelay)
		}

		text, err := config.Encode(s.Doc(), "yaml")
		require.NoError(t, err)

		doc, err := config.Parse(text, "yaml")
		require.NoError(t, err)

		back, err := Decode(config.Value{Key: "event_stream", Any: doc})
		require.NoError(t, err, "%s", text)

		require.Equal(t, wire(t, s), wire(t, back), "%s", text)
		require.Equal(t,
			[]any{s.HasRetry, s.Retry, s.HasFixedEventDelay, s.FixedEventDelay, s.Heartbeat, s.KeepOpen, s.Resume},
			[]any{back.HasRetry, back.Retry, back.HasFixedEventDelay, back.FixedEventDelay, back.Heartbeat, back.KeepOpen, back.Resume})
	})
}
