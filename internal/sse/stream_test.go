package sse

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// read is a Read of a stream: what it returned, and when, from the start of the stream.
type read struct {
	at   time.Duration
	text string
}

// readAll reads s to its end, and returns each Read and when it returned. It runs in a synctest bubble, whose clock
// moves only while s waits.
func readAll(t *testing.T, s *Stream) []read {
	t.Helper()

	start := time.Now()
	buf := make([]byte, 1024)

	var reads []read
	for {
		n, err := s.Read(buf)
		if errors.Is(err, io.EOF) {
			return reads
		}

		require.NoError(t, err)
		reads = append(reads, read{at: time.Since(start), text: string(buf[:n])})
	}
}

// stream compiles s and starts its stream with ctx.
func stream(ctx context.Context, t *testing.T, s *Spec, lastEventID string) *Stream {
	t.Helper()

	e, err := Compile(s, nil)
	require.NoError(t, err)

	st, err := e.Stream(ctx, nil, lastEventID)
	require.NoError(t, err)

	return st
}

// data returns an event with text data.
func data(text string) *EventSpec {
	return &EventSpec{Data: []byte(text), HasData: true}
}

func TestStream_SendsEachEventAfterItsDelay(t *testing.T) {
	// An event's own delay wins over the stream's: a client sees the events trickle in as asked.
	synctest.Test(t, func(t *testing.T) {
		late := data("b")
		late.Delay, late.HasDelay = 2*time.Second, true

		s := stream(t.Context(), t, &Spec{
			Retry: time.Second, HasRetry: true,
			EventDelay: vlatte.Latency(time.Second),
			Events:     []*EventSpec{data("a"), late, data("c")},
		}, "")

		require.Equal(t, []read{
			{0, "retry: 1000\n\n"},
			{time.Second, "data: a\n\n"},
			{3 * time.Second, "data: b\n\n"},
			{4 * time.Second, "data: c\n\n"},
		}, readAll(t, s))
	})
}

func TestStream_SendsHeartbeatsWhileItWaits(t *testing.T) {
	// A wait longer than the heartbeat sends a comment each heartbeat, so proxies and clients see a live connection.
	synctest.Test(t, func(t *testing.T) {
		ev := data("a")
		ev.Delay, ev.HasDelay = 25*time.Second, true

		s := stream(t.Context(), t, &Spec{Heartbeat: 10 * time.Second, Events: []*EventSpec{ev}}, "")

		require.Equal(t, []read{
			{10 * time.Second, ": heartbeat\n"},
			{20 * time.Second, ": heartbeat\n"},
			{25 * time.Second, "data: a\n\n"},
		}, readAll(t, s))
	})
}

func TestStream_KeepsOpenUntilTheContextEnds(t *testing.T) {
	// A stream that keeps open ends with io.EOF when the client leaves: it got every event, and that isn't a failure.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
		defer cancel()

		s := stream(ctx, t, &Spec{KeepOpen: true, Heartbeat: 15 * time.Second, Events: []*EventSpec{data("a")}}, "")

		require.Equal(t, []read{
			{0, "data: a\n\n"},
			{15 * time.Second, ": heartbeat\n"},
			{30 * time.Second, ": heartbeat\n"},
		}, readAll(t, s))
	})
}

func TestStream_KeepsOpenWithoutHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		s := stream(ctx, t, &Spec{KeepOpen: true, Events: []*EventSpec{data("a")}}, "")

		start := time.Now()
		require.Equal(t, []read{{0, "data: a\n\n"}}, readAll(t, s))
		require.Equal(t, time.Minute, time.Since(start))
	})
}

func TestStream_ClientLeavingEndsTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		late := data("b")
		late.Delay, late.HasDelay = time.Hour, true

		s := stream(ctx, t, &Spec{Events: []*EventSpec{data("a"), late}}, "")

		require.Equal(t, []read{{0, "data: a\n\n"}}, readAll(t, s))
	})
}

func TestStream_ResumesAfterTheLastEventID(t *testing.T) {
	// A client that reconnects with a Last-Event-ID gets what it missed. The retry field comes first again.
	withID := func(id, text string) *EventSpec {
		ev := data(text)
		ev.ID, ev.HasID = id, true

		return ev
	}

	events := []*EventSpec{withID("1", "a"), withID("2", "b"), withID("3", "c")}

	tests := []struct {
		name        string
		resume      bool
		lastEventID string
		want        []string
	}{
		{"after the id", true, "2", []string{"retry: 0\n\n", "id: 3\ndata: c\n\n"}},
		{"after the last", true, "3", []string{"retry: 0\n\n"}},
		{"unknown id replays all", true, "9", []string{"retry: 0\n\n", "id: 1\ndata: a\n\n", "id: 2\ndata: b\n\n", "id: 3\ndata: c\n\n"}},
		{"not resuming replays all", false, "2", []string{"retry: 0\n\n", "id: 1\ndata: a\n\n", "id: 2\ndata: b\n\n", "id: 3\ndata: c\n\n"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := stream(t.Context(), t, &Spec{HasRetry: true, Resume: tc.resume, Events: events}, tc.lastEventID)

				var got []string
				for _, r := range readAll(t, s) {
					got = append(got, r.text)
				}

				require.Equal(t, tc.want, got)
			})
		})
	}
}

func TestStream_ReadsAfterCloseEnd(t *testing.T) {
	s := stream(t.Context(), t, &Spec{Events: []*EventSpec{data("a")}}, "")
	require.NoError(t, s.Close())

	n, err := s.Read(make([]byte, 8))
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
}

func TestStream_ShortReadsGetTheRestOfTheEvent(t *testing.T) {
	// A Read with a short buffer gets the start of the event, and the next ones the rest, before the next event.
	s := stream(t.Context(), t, &Spec{Events: []*EventSpec{data("abcdef"), data("g")}}, "")

	buf := make([]byte, 3)
	n, err := s.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "dat", string(buf[:n]))

	rest, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, "a: abcdef\n\ndata: g\n\n", string(rest))
}
