package handler_test

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
)

// sse is a client of a Server-Sent Events route.
type sse struct {
	res    *http.Response
	lines  *bufio.Reader
	cancel context.CancelFunc
}

// listen opens a Server-Sent Events route, and returns once the server answered: it publishes to the client from then.
func listen(t *testing.T, url string) *sse {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

	return &sse{res: res, lines: bufio.NewReader(res.Body), cancel: cancel}
}

// next reads the next event: its type and its data, the lines of data joined.
func (c *sse) next(t *testing.T) (string, string) {
	t.Helper()

	var event string
	var data []string

	for {
		line, err := c.lines.ReadString('\n')
		require.NoError(t, err)

		line = strings.TrimSuffix(line, "\n")

		switch {
		case line == "":
			return event, strings.Join(data, "\n")
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
}

func TestEvents(t *testing.T) {
	// A client follows what the mock server serves as it happens, each client all of it, without a journal of
	// requests to poll.
	events := handler.NewEvents()
	url := serve(t, newStore(t), admin.Events(events))

	first, second := listen(t, url+"/events"), listen(t, url+"/events")

	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	events.Publish(handler.Event{Type: handler.EventMatched, Time: at, Method: "GET", Target: "/hello", MockID: "hello", Status: 200})
	events.Publish(handler.Event{Type: handler.EventUnmatched, Time: at, Method: "POST", Target: "/nope"})

	for _, c := range []*sse{first, second} {
		event, data := c.next(t)
		require.Equal(t, "matched", event)
		conformsAs(t, data, "json", "Event")
		require.JSONEq(t, `{"type": "matched", "time": "2026-10-20T12:00:00Z", "method": "GET", "target": "/hello", "mock_id": "hello", "status": 200}`, data)

		event, data = c.next(t)
		require.Equal(t, "unmatched", event)

		var e handler.Event
		require.NoError(t, json.Unmarshal([]byte(data), &e))
		require.Equal(t, "/nope", e.Target)
	}
}

func TestEvents_AClientThatLeavesStopsGettingThem(t *testing.T) {
	// Publishers never wait on clients: one that left, or falls behind, misses events, and the others don't.
	events := handler.NewEvents()
	url := serve(t, newStore(t), admin.Events(events))

	gone, stays := listen(t, url+"/events"), listen(t, url+"/events")
	gone.cancel()

	for range 1000 {
		events.Publish(handler.Event{Type: handler.EventWarning, Message: "w"})
	}

	event, _ := stays.next(t)
	require.Equal(t, "warning", event)
}

func TestEvents_CloseEndsTheStreams(t *testing.T) {
	// A stream never ends on its own: closing the admin API ends it, so nothing of the admin API outlives Close.
	events := handler.NewEvents()

	s, err := admin.New(admin.Mocks[*fakeMock](newStore(t), &fakeBuilder{}), admin.Events(events))
	require.NoError(t, err)
	require.NoError(t, s.Start())

	c := listen(t, s.URL()+"/events")

	done := make(chan error, 1)
	go func() { done <- s.Close() }()

	_, err = io.ReadAll(c.res.Body)
	require.NoError(t, err, "the stream ends")

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on the stream")
	}
}

func TestEvents_AClosedStreamHasNone(t *testing.T) {
	// Once the events close, as the admin API does, a client that comes gets no stream that would never end.
	events := handler.NewEvents()
	url := serve(t, newStore(t), admin.Events(events))

	events.Close()

	res, body := send(t, request{method: http.MethodGet, url: url + "/events"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
}

func TestEvents_NotSupported(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: url + "/events"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't stream its events", doc["detail"])
}
