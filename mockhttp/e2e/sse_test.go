package e2e

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// event is an event as EventSource dispatches it.
type event struct {
	Type string
	Data string
	ID   string // the last event ID once the event was dispatched
}

// eventSource reads an event stream as EventSource does, line by line, and dispatches an event at each blank line
// that ends one with data. It keeps what a client keeps between events: the last event ID and the reconnection time.
// It keeps the comments too, which a client skips, for a test to see.
type eventSource struct {
	r        *bufio.Reader
	started  bool // the first line was read, and lost its byte order mark
	afterCR  bool // the last line ended with CR: an LF right after it ends no line
	idBuffer string
	lastID   string
	retry    time.Duration
	comments []string
}

// next returns the next event the stream dispatches. It returns the error that ended the stream first: io.EOF, or
// io.ErrUnexpectedEOF when the connection dropped. An event the stream didn't end is dropped, as EventSource drops it.
func (es *eventSource) next() (event, error) {
	var (
		data      strings.Builder
		eventType string
		hasData   bool
	)

	for {
		line, err := es.line()
		if err != nil {
			return event{}, err
		}

		if line == "" {
			es.lastID = es.idBuffer
			if hasData {
				return event{Type: cmp.Or(eventType, "message"), Data: strings.TrimSuffix(data.String(), "\n"), ID: es.lastID}, nil
			}

			eventType = ""

			continue
		}

		if comment, ok := strings.CutPrefix(line, ":"); ok {
			es.comments = append(es.comments, strings.TrimPrefix(comment, " "))
			continue
		}

		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")

		switch name {
		case "event":
			eventType = value
		case "data":
			data.WriteString(value + "\n")
			hasData = true
		case "id":
			if !strings.ContainsRune(value, 0) {
				es.idBuffer = value
			}
		case "retry":
			if ms, err := strconv.Atoi(value); err == nil && strings.Trim(value, "0123456789") == "" {
				es.retry = time.Duration(ms) * time.Millisecond
			}
		}
	}
}

// line reads a line, without the CRLF, CR or LF that ends it. The first line of the stream loses its byte order mark.
func (es *eventSource) line() (string, error) {
	var b []byte

	for {
		c, err := es.r.ReadByte()
		if err != nil {
			return "", err
		}

		if es.afterCR {
			es.afterCR = false
			if c == '\n' {
				continue
			}
		}

		if c != '\n' && c != '\r' {
			b = append(b, c)
			continue
		}

		es.afterCR = c == '\r'

		line := string(b)
		if !es.started {
			es.started = true
			line = strings.TrimPrefix(line, "\uFEFF")
		}

		return line, nil
	}
}

// sendForStream sends req with client and returns its response, which must be an event stream, and a reader of it.
func sendForStream(t *testing.T, client *http.Client, req *http.Request) (*http.Response, *eventSource) {
	t.Helper()

	req.Header.Set(mockhttp.HeaderAccept, mockhttp.MIMETextEventStream)

	res, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, mockhttp.MIMETextEventStream, res.Header.Get(mockhttp.HeaderContentType))

	return res, &eventSource{r: bufio.NewReader(res.Body)}
}

// openStream sends a GET of url with client and ctx, as EventSource does: with lastEventID as its Last-Event-ID
// when it is set.
func openStream(ctx context.Context, t *testing.T, client *http.Client, url, lastEventID string) (*http.Response, *eventSource) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	if lastEventID != "" {
		req.Header.Set(mockhttp.HeaderLastEventID, lastEventID)
	}

	return sendForStream(t, client, req)
}

// readAll reads es to its end, which must be io.EOF, and returns the events it dispatched.
func readAll(t *testing.T, es *eventSource) []event {
	t.Helper()

	var events []event
	for {
		ev, err := es.next()
		if errors.Is(err, io.EOF) {
			return events
		}

		require.NoError(t, err)
		events = append(events, ev)
	}
}

func TestSSE_ReconnectsAfterADropAndResumes(t *testing.T) {
	// The connection drops after the first event. The client waits the reconnection time the stream set, reconnects
	// with the id of the last event it got, and the stream resumes after it: nothing is lost, nothing sent twice.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	stream := mockhttp.NewEventStream(
		mockhttp.NewEvent().ID("1").Data("a"),
		mockhttp.NewEvent().ID("2").Data("b"),
		mockhttp.NewEvent().ID("3").Data("c"),
	).Retry(1500 * time.Millisecond).ResumeFromLastEventID()

	m.MustMock(mockhttp.Getf("/events").Once().
		Step(fault.LimitData(len("retry: 1500\n\nid: 1\ndata: a\n\n"))).
		Reply(mockhttp.OK().EventStream(stream)))
	m.MustMock(mockhttp.Getf("/events").
		Header(mockhttp.HeaderLastEventID, match.Equal("1")).
		Reply(mockhttp.OK().EventStream(stream)))

	client := newClient(t)

	_, first := openStream(t.Context(), t, client, m.URL("/events"), "")

	ev, err := first.next()
	require.NoError(t, err)
	require.Equal(t, event{Type: "message", Data: "a", ID: "1"}, ev)
	require.Equal(t, 1500*time.Millisecond, first.retry)

	_, err = first.next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "the connection dropped")

	_, second := openStream(t.Context(), t, client, m.URL("/events"), first.lastID)
	require.Equal(t, []event{{"message", "b", "2"}, {"message", "c", "3"}}, readAll(t, second))
}

func TestSSE_AKeepOpenStreamEndsWhenTheClientLeaves(t *testing.T) {
	// A stream that keeps open serves the client as long as it stays. Once it leaves, the mock counts the request and
	// runs its callbacks, with what the client got.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	ended := make(chan string, 1)
	scoped := m.MustMock(mockhttp.Getf("/events").
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Data("hi")).KeepOpen())).
		Callback(func(_ context.Context, in vlatte.OnComplete) error {
			ended <- string(in.(*mockhttp.OnComplete).Response().Body)
			return nil
		}))

	ctx, leave := context.WithCancel(t.Context())
	_, c := openStream(ctx, t, newClient(t), m.URL("/events"), "")

	ev, err := c.next()
	require.NoError(t, err)
	require.Equal(t, event{Type: "message", Data: "hi"}, ev)
	require.EqualValues(t, 0, scoped.Hits(), "the stream is still open")

	leave()

	require.Equal(t, "data: hi\n\n", <-ended)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestSSE_ServerCloseEndsAKeepOpenStream(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()

	m.MustMock(mockhttp.Getf("/events").
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Data("hi")).KeepOpen())))

	_, c := openStream(t.Context(), t, newClient(t), m.URL("/events"), "")

	_, err := c.next()
	require.NoError(t, err)

	m.Close()

	_, err = c.next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "the stream ended with the server")
}

func TestSSE_OverHTTP2(t *testing.T) {
	// HTTP/2 has no chunked encoding: the stream goes as data frames, still one flush per event.
	cert, err := tls.LoadX509KeyPair(_tlsClientCertFile, _tlsClientKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(_tlsCertFile)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}},
		Protocols:       new(http.Protocols),
	}
	tr.Protocols.SetHTTP2(true)
	t.Cleanup(tr.CloseIdleConnections)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	t.Cleanup(m.Close)
	m.MustStartTLS()

	m.MustMock(mockhttp.Getf("/events").
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Name("tick").Data("1")).KeepOpen())))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	res, c := openStream(ctx, t, &http.Client{Transport: tr}, m.URL("/events"), "")
	require.Equal(t, "HTTP/2.0", res.Proto)

	ev, err := c.next()
	require.NoError(t, err)
	require.Equal(t, event{Type: "tick", Data: "1"}, ev)
}

func TestSSE_FlushesEachEvent(t *testing.T) {
	// A stream that keeps open never fills the server's buffer: a client gets each event as it is sent, gzipped or
	// not, or live updates would never arrive.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	stream := mockhttp.NewEventStream(mockhttp.NewEvent().Data("first")).KeepOpen()
	m.MustMock(mockhttp.Getf("/plain").Reply(mockhttp.OK().EventStream(stream)))
	m.MustMock(mockhttp.Getf("/gzip").Reply(mockhttp.OK().Gzip().EventStream(stream)))

	for _, path := range []string{"/plain", "/gzip"} {
		t.Run(path, func(t *testing.T) {
			ctx, leave := context.WithCancel(t.Context())
			defer leave()

			_, c := openStream(ctx, t, newClient(t), m.URL(path), "")

			ev, err := c.next()
			require.NoError(t, err)
			require.Equal(t, event{Type: "message", Data: "first"}, ev)
		})
	}
}

func TestSSE_SendsTheHeadersBeforeTheFirstEvent(t *testing.T) {
	// EventSource opens once it has the response headers. A stream with nothing to send yet, such as one that keeps
	// open with no events, sends them at once, gzipped or not, or the client would never open.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	stream := mockhttp.NewEventStream().KeepOpen()
	m.MustMock(mockhttp.Getf("/plain").Reply(mockhttp.OK().EventStream(stream)))
	m.MustMock(mockhttp.Getf("/gzip").Reply(mockhttp.OK().Gzip().EventStream(stream)))

	for _, path := range []string{"/plain", "/gzip"} {
		t.Run(path, func(t *testing.T) {
			// The deadline only bounds a server that never sends the headers: openStream fails then.
			ctx, leave := context.WithTimeout(t.Context(), 10*time.Second)
			defer leave()

			openStream(ctx, t, newClient(t), m.URL(path), "")
		})
	}
}

func TestSSE_FromAMockFile(t *testing.T) {
	// A mock file declares an event stream under response.event_stream: data that isn't text is JSON, raw is sent as
	// it is, and template.enabled makes the text templates. A Content-Type header wins over text/event-stream, as one
	// with a charset.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromBytes([]byte(`
request: {method: GET, path: /events}
response:
  status: 200
  header: {content-type: text/event-stream; charset=utf-8}
  event_stream:
    retry: 2s
    resume: true
    events:
      - {event: price, id: "1", data: {value: 10}}
      - {comment: note, data: "for {{ .Request.URL.Query.Get \"user\" }}", delay: 1ms}
      - {raw: "data: crlf\r\n\r\n"}
  template: {enabled: true}
`), "yaml"))

	res, body := getTarget(t, m, "/events?user=ana")
	require.Equal(t, "text/event-stream; charset=utf-8", res.Header.Get(mockhttp.HeaderContentType))
	require.Equal(t, "retry: 2000\n\nevent: price\nid: 1\ndata: {\"value\":10}\n\n: note\ndata: for ana\n\ndata: crlf\r\n\r\n", string(body))
}

func TestSSE_ADescriptionLoadsBackAndServesTheSame(t *testing.T) {
	// A mock the admin API or a store keeps as its description serves the same stream once built back.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(mockhttp.Getf("/a").ID("a").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Name("e").ID("1").DataJSON([]any{1, "two"}),
		mockhttp.NewEvent().Comment("c\nd").Data("multi\nline").Delay(time.Millisecond),
		mockhttp.NewEvent().DataJSON(nil),
		mockhttp.NewEvent().DataJSON(uint64(18446744073709551615)),
		mockhttp.NewEvent().Raw(": raw\n"),
	).Retry(time.Second))))

	data, err := mockhttp.EncodeMock(scope.Mock("a"), "yaml")
	require.NoError(t, err)

	back, err := mockhttp.DecodeMock(m, data, "yaml")
	require.NoError(t, err)
	t.Cleanup(func() { _ = back.Close() })

	again, err := mockhttp.EncodeMock(back, "yaml")
	require.NoError(t, err)
	require.Equal(t, string(data), string(again))

	other := mockhttp.MustNew(mockhttp.Setup().Silent())
	other.MustStart()
	t.Cleanup(other.Close)
	other.MustMock(mockhttp.FromBytes(data, "yaml"))

	_, want := getTarget(t, m, "/a")
	_, got := getTarget(t, other, "/a")
	require.Equal(t, "retry: 1000\n\nevent: e\nid: 1\ndata: [1,\"two\"]\n\n: c\n: d\ndata: multi\ndata: line\n\ndata: null\n\ndata: 18446744073709551615\n\n: raw\n", string(want))
	require.Equal(t, string(want), string(got))
}

func TestSSE_StreamsAChatCompletionAsOpenAIDoes(t *testing.T) {
	// An OpenAI-style API streams a chat completion when the request asks for it: chunks of JSON, each with a piece of
	// the answer, then [DONE]. The client puts the answer together as the chunks arrive. The same endpoint answers a
	// request that doesn't stream with the whole completion.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	chunk := func(content string) *mockhttp.Event {
		return mockhttp.NewEvent().DataJSON(map[string]any{
			"object":  "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}}},
		})
	}

	m.MustMock(mockhttp.Postf("/v1/chat/completions").
		Header(mockhttp.HeaderAuthorization, match.Equal("Bearer sk-test")).
		Body(match.JSONPath("$.stream", match.Equal(true))).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			chunk("Hel"), chunk("lo, "), chunk("world"),
			mockhttp.NewEvent().Data("[DONE]"),
		).EventDelay(time.Millisecond))))
	m.MustMock(mockhttp.Postf("/v1/chat/completions").
		Header(mockhttp.HeaderAuthorization, match.Equal("Bearer sk-test")).
		Body(match.JSONPath("$.stream", match.Equal(false))).
		Reply(mockhttp.OK().JSON(map[string]any{
			"object":  "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Hello, world"}}},
		})))

	ask := func(stream bool) *http.Request {
		body := fmt.Sprintf(`{"model": "gpt-test", "stream": %t, "messages": [{"role": "user", "content": "Say hello"}]}`, stream)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL("/v1/chat/completions"), strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
		req.Header.Set(mockhttp.HeaderAuthorization, "Bearer sk-test")

		return req
	}

	_, es := sendForStream(t, newClient(t), ask(true))

	var (
		answer strings.Builder
		chunks int
	)

	for {
		ev, err := es.next()
		require.NoError(t, err)

		if ev.Data == "[DONE]" {
			break
		}

		var c struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		require.NoError(t, json.Unmarshal([]byte(ev.Data), &c))
		answer.WriteString(c.Choices[0].Delta.Content)
		chunks++
	}

	require.Equal(t, "Hello, world", answer.String())
	require.Equal(t, 3, chunks)

	_, err := es.next()
	require.ErrorIs(t, err, io.EOF, "the stream ends after [DONE]")

	res, err := newClient(t).Do(ask(false))
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.MIMEApplicationJSON, res.Header.Get(mockhttp.HeaderContentType))
	require.JSONEq(t, `{"object": "chat.completion", "choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello, world"}}]}`, string(body))
}

func TestSSE_StreamsAMessageAsAnthropicDoes(t *testing.T) {
	// An Anthropic-style API names each event after the type of its data. A client dispatches on the name: it builds
	// the text from the deltas, skips the pings, and stops at message_stop. The mock is a mock file, as a team would
	// keep it.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromBytes([]byte(`
request:
  method: POST
  path: /v1/messages
  header:
    x-api-key: test-key
    anthropic-version: "2023-06-01"
  body: [jsonpath, $.stream, [equal, true]]
response:
  status: 200
  event_stream:
    events:
      - {event: message_start, data: {type: message_start, message: {id: msg_1, role: assistant, content: []}}}
      - {event: content_block_start, data: {type: content_block_start, index: 0, content_block: {type: text, text: ""}}}
      - {event: ping, data: {type: ping}}
      - {event: content_block_delta, data: {type: content_block_delta, index: 0, delta: {type: text_delta, text: Hello}}}
      - {event: content_block_delta, data: {type: content_block_delta, index: 0, delta: {type: text_delta, text: ", world"}}}
      - {event: content_block_stop, data: {type: content_block_stop, index: 0}}
      - {event: message_delta, data: {type: message_delta, delta: {stop_reason: end_turn}}}
      - {event: message_stop, data: {type: message_stop}}
`), "yaml"))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL("/v1/messages"),
		strings.NewReader(`{"model": "claude-test", "max_tokens": 64, "stream": true, "messages": [{"role": "user", "content": "Say hello"}]}`))
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("anthropic-version", "2023-06-01")

	_, es := sendForStream(t, newClient(t), req)

	var (
		types []string
		text  strings.Builder
	)

	for {
		ev, err := es.next()
		require.NoError(t, err)

		var data struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		require.NoError(t, json.Unmarshal([]byte(ev.Data), &data))
		require.Equal(t, ev.Type, data.Type, "each event is named after the type of its data")

		types = append(types, ev.Type)
		if ev.Type == "content_block_delta" {
			text.WriteString(data.Delta.Text)
		}

		if ev.Type == "message_stop" {
			break
		}
	}

	require.Equal(t, []string{
		"message_start", "content_block_start", "ping", "content_block_delta", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}, types)
	require.Equal(t, "Hello, world", text.String())
}

func TestSSE_HeartbeatsKeepAQuietFeedAlive(t *testing.T) {
	// Proxies and load balancers drop a connection that stays silent. A live feed sends a comment while it waits for
	// its next event, which clients skip, so a quiet stretch doesn't cost the connection.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/prices").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Name("price").Data("10"),
		mockhttp.NewEvent().Name("price").Data("11").Delay(30*time.Millisecond),
	).Heartbeat(10 * time.Millisecond))))

	_, es := openStream(t.Context(), t, newClient(t), m.URL("/prices"), "")

	require.Equal(t, []event{{Type: "price", Data: "10"}, {Type: "price", Data: "11"}}, readAll(t, es))
	require.Equal(t, []string{"heartbeat", "heartbeat"}, es.comments, "a heartbeat each 10ms of the 30ms wait, but the last")
}

func TestSSE_EachUserGetsTheirOwnNotifications(t *testing.T) {
	// A notification feed at /users/{id}/notifications serves a signed-in user the notifications the path names. A
	// request without a token gets 401 before any stream starts, which makes EventSource give up rather than retry.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Request().Method(http.MethodGet).
		URLPathTemplate("/users/{id}/notifications").
		Header(mockhttp.HeaderAuthorization, match.HasPrefix("Bearer ")).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			mockhttp.NewEvent().Name("notification").ID("{{ .Request.PathParams.id }}-1").
				Data(`{"user": "{{ .Request.PathParams.id }}", "text": "Your order shipped"}`),
		).KeepOpen().Template())))
	m.MustMock(mockhttp.Request().Method(http.MethodGet).
		URLPathTemplate("/users/{id}/notifications").
		Priority(1).
		Reply(mockhttp.Unauthorized().PlainText("sign in first")))

	for _, user := range []string{"ana", "rui"} {
		t.Run(user, func(t *testing.T) {
			ctx, leave := context.WithCancel(t.Context())
			defer leave()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/users/"+user+"/notifications"), nil)
			require.NoError(t, err)
			req.Header.Set(mockhttp.HeaderAuthorization, "Bearer token-"+user)

			_, es := sendForStream(t, newClient(t), req)

			ev, err := es.next()
			require.NoError(t, err)
			require.Equal(t, event{Type: "notification", Data: `{"user": "` + user + `", "text": "Your order shipped"}`, ID: user + "-1"}, ev)
		})
	}

	res, err := newClient(t).Get(m.URL("/users/ana/notifications"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, mockhttp.MIMETextPlain, res.Header.Get(mockhttp.HeaderContentType))
}

func TestSSE_A204TellsTheClientToStopReconnecting(t *testing.T) {
	// A job streams its progress until it is done, and the stream ends. EventSource reconnects after any end; the
	// server answers that reconnection with 204 No Content, which tells EventSource to stop.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/jobs/7/progress").Once().Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Name("progress").ID("1").DataJSON(map[string]any{"percent": 10}),
		mockhttp.NewEvent().Name("progress").ID("2").DataJSON(map[string]any{"percent": 60}),
		mockhttp.NewEvent().Name("done").ID("3").DataJSON(map[string]any{"percent": 100}),
	).Retry(100 * time.Millisecond))))
	reconnected := m.MustMock(mockhttp.Getf("/jobs/7/progress").
		Header(mockhttp.HeaderLastEventID, match.Equal("3")).
		Reply(mockhttp.NoContent()))

	client := newClient(t)

	_, es := openStream(t.Context(), t, client, m.URL("/jobs/7/progress"), "")
	require.Equal(t, []event{
		{"progress", `{"percent":10}`, "1"},
		{"progress", `{"percent":60}`, "2"},
		{"done", `{"percent":100}`, "3"},
	}, readAll(t, es))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/jobs/7/progress"), nil)
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderAccept, mockhttp.MIMETextEventStream)
	req.Header.Set(mockhttp.HeaderLastEventID, es.lastID)

	res, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, body)
	require.EqualValues(t, 1, reconnected.Hits())
}

func TestSSE_AProxiedStreamArrivesEventByEvent(t *testing.T) {
	// A mock that forwards to a real event source passes each event on as the source sends it, not once the source
	// is done: the client gets the first event while the second doesn't exist yet.
	second := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(mockhttp.HeaderContentType, mockhttp.MIMETextEventStream)
		_, _ = io.WriteString(w, "id: 1\ndata: first\n\n")
		w.(http.Flusher).Flush()

		select {
		case <-second:
			_, _ = io.WriteString(w, "id: 2\ndata: second\n\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/events").Reply(mockhttp.From(upstream.URL)))

	_, es := openStream(t.Context(), t, newClient(t), m.URL("/events"), "")

	ev, err := es.next()
	require.NoError(t, err)
	require.Equal(t, event{Type: "message", Data: "first", ID: "1"}, ev)

	close(second)

	require.Equal(t, []event{{Type: "message", Data: "second", ID: "2"}}, readAll(t, es))
}

func TestSSE_EveryClientGetsItsOwnStream(t *testing.T) {
	// Every open tab of a dashboard has its own EventSource on the same feed. Each gets the whole stream, at once,
	// and the mock counts each one once its client leaves.
	const clients = 8

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	ended := make(chan struct{}, clients)
	feed := m.MustMock(mockhttp.Getf("/feed").
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			mockhttp.NewEvent().ID("1").Data("a"),
			mockhttp.NewEvent().ID("2").Data("b"),
		).KeepOpen())).
		Callback(func(context.Context, vlatte.OnComplete) error {
			ended <- struct{}{}
			return nil
		}))

	streams := make([]*eventSource, clients)
	leaves := make([]context.CancelFunc, clients)

	for i := range clients {
		ctx, leave := context.WithCancel(t.Context())
		leaves[i] = leave
		_, streams[i] = openStream(ctx, t, newClient(t), m.URL("/feed"), "")
	}

	got := make([][]event, clients)
	errs := make([]error, clients)

	var wg sync.WaitGroup
	for i, es := range streams {
		wg.Go(func() {
			for range 2 {
				ev, err := es.next()
				if err != nil {
					errs[i] = err
					return
				}

				got[i] = append(got[i], ev)
			}
		})
	}
	wg.Wait()

	want := []event{{"message", "a", "1"}, {"message", "b", "2"}}
	for i := range clients {
		require.NoError(t, errs[i])
		require.Equal(t, want, got[i], "client %d", i)
	}

	require.EqualValues(t, 0, feed.Hits(), "every stream is still open")

	for _, leave := range leaves {
		leave()
	}

	for range clients {
		<-ended
	}

	require.EqualValues(t, clients, feed.Hits())
}

func TestSSE_ABrowserOnAnotherOriginReadsTheStream(t *testing.T) {
	// new EventSource(url, {withCredentials: true}) on https://app.example sends its Origin and cookies. The browser
	// lets the page read the stream only if the answer allows that origin, with credentials.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().CORS(&mockhttp.CORSConfig{
		AllowedOrigin:    "https://app.example",
		AllowCredentials: true,
	}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/events").
		Cookie("session", match.Equal("s1")).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Data("hi")).KeepOpen())))

	ctx, leave := context.WithCancel(t.Context())
	defer leave()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/events"), nil)
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderOrigin, "https://app.example")
	req.AddCookie(&http.Cookie{Name: "session", Value: "s1"})

	res, es := sendForStream(t, newClient(t), req)
	require.Equal(t, "https://app.example", res.Header.Get(mockhttp.HeaderAccessControlAllowOrigin))
	require.Equal(t, "true", res.Header.Get(mockhttp.HeaderAccessControlAllowCredentials))

	ev, err := es.next()
	require.NoError(t, err)
	require.Equal(t, event{Type: "message", Data: "hi"}, ev)
}

func TestSSE_RawEventsTestAClientsParser(t *testing.T) {
	// raw sends what a server in the wild may send, to test a client's parser: a byte order mark, CRLF and CR line
	// endings, a field without a colon or a space, comments, an unknown field, a retry that isn't a number, an id
	// with NUL, and an event that sets an id but has no data. A client that follows the standard dispatches these.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/events").Reply(mockhttp.OK().Gzip().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Raw("\uFEFFdata: bom\r\n\r\n"),
		mockhttp.NewEvent().Raw("event: cr\rdata:no space\rdata\r\r"),
		mockhttp.NewEvent().Raw(": comment\nunknown: x\nretry: soon\nid: a\x00b\ndata: kept\n\n"),
		mockhttp.NewEvent().Raw("id: 7\n\n"),
		mockhttp.NewEvent().Raw("data:  two spaces\n\n"),
	))))

	_, es := openStream(t.Context(), t, newClient(t), m.URL("/events"), "")

	require.Equal(t, []event{
		{Type: "message", Data: "bom"},
		{Type: "cr", Data: "no space\n"},
		{Type: "message", Data: "kept"},
		{Type: "message", Data: " two spaces", ID: "7"},
	}, readAll(t, es))
	require.Equal(t, []string{"comment"}, es.comments)
	require.Zero(t, es.retry, "a retry that isn't a number is ignored")
}
