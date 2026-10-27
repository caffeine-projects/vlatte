package mockhttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mpart"
)

// eventWriter is a response writer that logs what rendering does to it.
type eventWriter struct {
	header http.Header
	events []string
	body   bytes.Buffer
}

func (w *eventWriter) Header() http.Header { return w.header }
func (w *eventWriter) WriteHeader(_ int)   { w.events = append(w.events, "header") }
func (w *eventWriter) Flush()              { w.events = append(w.events, "flush") }
func (w *eventWriter) Write(p []byte) (int, error) {
	w.events = append(w.events, "write")
	return w.body.Write(p)
}

// readable returns what a client can read of the body written so far: the bytes, or what gunzips of them.
func (w *eventWriter) readable(t require.TestingT, gzipped bool) []byte {
	if !gzipped {
		return slices.Clone(w.body.Bytes())
	}

	if w.body.Len() == 0 {
		return nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(w.body.Bytes()))
	require.NoError(t, err)

	// The stream isn't over until the end: what is flushed reads, then the reader runs out.
	b, _ := io.ReadAll(gz)

	return b
}

func TestPropStream_EachPauseFollowsAFlush(t *testing.T) {
	// M21: a streamed body waits at each pause only once everything before it is written and flushed, through gzip
	// and steps too, so the client reads it during the wait. When the client goes away during a wait, nothing more
	// is written. No real time passes: the wait is a fake.
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		body := []byte(rapid.StringN(1, 200, -1).Draw(t, "body"))
		gzipped := rapid.Bool().Draw(t, "gzip")
		withSteps := rapid.Bool().Draw(t, "steps")

		// Offsets past the end stand for a body a Mapper shortened.
		offsets := rapid.SliceOfNDistinct(rapid.IntRange(1, len(body)+10), 1, 4, func(n int) int { return n }).Draw(t, "pauses")
		slices.Sort(offsets)

		pauses := make([]mpart.Pause, len(offsets))
		for i, at := range offsets {
			pauses[i] = mpart.Pause{At: at, Delay: time.Duration(rapid.Int64Range(1, int64(time.Hour)).Draw(t, "delay"))}
		}

		cancelAt := -1
		if rapid.Bool().Draw(t, "client goes away") {
			cancelAt = rapid.IntRange(0, len(pauses)-1).Draw(t, "goes away at")
		}

		res := &Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, pauses: pauses}
		if gzipped {
			res.Encoding = "gzip"
		}

		ew := &eventWriter{header: make(http.Header)}

		var w http.ResponseWriter = ew
		if withSteps {
			w = newTopResponseWriter(context.Background(), []vlatte.Step{appendStep("")}, ew)
		}

		waits := 0
		fakeWait := func(_ context.Context, d time.Duration) error {
			p := pauses[waits]
			require.Equal(t, p.Delay, d, "pause %d", waits)
			require.Equal(t, "flush", ew.events[len(ew.events)-1], "pause %d follows a flush", waits)
			require.Equal(t, string(body[:min(p.At, len(body))]), string(ew.readable(t, gzipped)), "pause %d: the client reads what comes before it", waits)

			ew.events = append(ew.events, "wait")

			if waits == cancelAt {
				return context.Canceled
			}

			waits++

			return nil
		}

		err := res.render(context.Background(), w, fakeWait)

		require.Equal(t, "header", ew.events[0])

		if cancelAt >= 0 {
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, "wait", ew.events[len(ew.events)-1], "nothing is written after the client went away")

			return
		}

		require.NoError(t, err)
		require.Equal(t, len(pauses), waits)
		require.Equal(t, string(body), string(ew.readable(t, gzipped)))
		require.Equal(t, "flush", ew.events[len(ew.events)-1], "the last part goes out now, not when the handler returns")
	})
}

// failingWriter is a response writer whose writes, or flushes, fail, as when the client went away.
type failingWriter struct {
	header    http.Header
	writes    int
	flushes   int
	failWrite int // the write that fails, from 1; 0 never
	failFlush int // the flush that fails, from 1; 0 never
}

func (w *failingWriter) Header() http.Header { return w.header }
func (w *failingWriter) WriteHeader(int)     {}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failWrite {
		return 0, errors.New("broken pipe")
	}

	return len(p), nil
}

func (w *failingWriter) FlushError() error {
	w.flushes++
	if w.flushes == w.failFlush {
		return errors.New("flush failed")
	}

	return nil
}

func TestStream_ErrorsEndIt(t *testing.T) {
	// A streamed body stops at the first write or flush that fails, and says why.
	pauses := []mpart.Pause{{At: 2, Delay: time.Hour}, {At: 4, Delay: time.Hour}}
	noWait := func(context.Context, time.Duration) error { return nil }

	tests := []struct {
		name     string
		encoding string
		w        *failingWriter
		want     string
	}{
		{"a segment", "", &failingWriter{failWrite: 1}, "broken pipe"},
		{"the rest", "", &failingWriter{failWrite: 3}, "broken pipe"},
		{"a flush", "", &failingWriter{failFlush: 1}, "flush failed"},
		{"a gzipped segment", "gzip", &failingWriter{failWrite: 1}, "broken pipe"},
		// gzip writes its header first, and holds the segment until it is flushed.
		{"a gzipped segment, when it is flushed", "gzip", &failingWriter{failWrite: 2}, "broken pipe"},
		{"a gzipped flush", "gzip", &failingWriter{failFlush: 1}, "flush failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.w.header = make(http.Header)
			res := &Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte("abcdef"), pauses: pauses, Encoding: tc.encoding}
			require.EqualError(t, res.render(context.Background(), tc.w, noWait), tc.want)
		})
	}
}
