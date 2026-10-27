package mockhttp

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestReplyEventStream_ServesItsEvents(t *testing.T) {
	// A client reads the events as the event stream format writes them, under the media type EventSource requires,
	// and with caching off, as live streams are.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").Reply(OK().EventStream(NewEventStream(
		NewEvent().Name("price").ID("1").DataJSON(map[string]any{"value": 10}),
		NewEvent().Comment("note").Data("line 1\nline 2").Retry(time.Second).Delay(0),
	).Retry(3 * time.Second).Heartbeat(time.Hour))))

	res, body := fetch(t, m.URL("/events"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, MIMETextEventStream, res.Header.Get(HeaderContentType))
	require.Equal(t, "no-cache", res.Header.Get(HeaderCacheControl))
	require.Empty(t, res.Header.Get(HeaderContentLength), "a stream has no length")
	require.Equal(t, "retry: 3000\n\nevent: price\nid: 1\ndata: {\"value\":10}\n\n: note\nretry: 1000\ndata: line 1\ndata: line 2\n\n", string(body))
}

func TestReplyEventStream_KeepsTheCacheControlItIsGiven(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").Reply(OK().Header(HeaderCacheControl, "no-store").EventStream(NewEventStream())))

	res, _ := fetch(t, m.URL("/events"))
	require.Equal(t, []string{"no-store"}, res.Header.Values(HeaderCacheControl))
}

// firstEvent requests url and reads the stream up to the blank line that ends its first event, while the stream
// keeps open: the event can only arrive if the server flushed it. Leaving ends the stream.
func firstEvent(t *testing.T, url string) string {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	var event string

	r := bufio.NewReader(res.Body)
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err)

		if event += line; line == "\n" {
			return event
		}
	}
}

func TestReplyEventStream_FlushesEachEvent(t *testing.T) {
	// A stream that keeps open never fills the server's buffer: a client gets each event as it is sent, gzipped or
	// not, or live updates would never arrive.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/plain").Reply(OK().EventStream(NewEventStream(NewEvent().Data("first")).KeepOpen())))
	m.MustMock(Getf("/gzip").Reply(OK().Gzip().EventStream(NewEventStream(NewEvent().Data("first")).KeepOpen())))

	require.Equal(t, "data: first\n\n", firstEvent(t, m.URL("/plain")))
	require.Equal(t, "data: first\n\n", firstEvent(t, m.URL("/gzip")))
}

func TestReplyEventStream_ResumesFromLastEventID(t *testing.T) {
	// A client that reconnects sends the id of the last event it got: a resuming stream sends only what it missed.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").Reply(OK().EventStream(NewEventStream(
		NewEvent().ID("1").Data("a"),
		NewEvent().ID("2").Data("b"),
	).ResumeFromLastEventID())))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/events"), nil)
	require.NoError(t, err)
	req.Header.Set(HeaderLastEventID, "1")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := readAll(res)
	require.NoError(t, err)
	require.Equal(t, "id: 2\ndata: b\n\n", string(body))

	_, body = fetch(t, m.URL("/events"))
	require.Equal(t, "id: 1\ndata: a\n\nid: 2\ndata: b\n\n", string(body), "a first connection gets every event")
}

func TestReplyEventStream_Templates(t *testing.T) {
	// A templated stream renders its text with the request and the reply's data; JSON data is sent as it is.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").Reply(OK().SetTemplateData(map[string]any{"id": 7}).EventStream(NewEventStream(
		NewEvent().Name(`{{ .Request.URL.Query.Get "topic" }}`).ID("{{ .Extra.id }}").Data("for {{ .Request.URL.Query.Get \"user\" }}"),
		NewEvent().DataJSON("{{ .Extra.id }}"),
	).Template())))

	_, body := fetch(t, m.URL("/events?topic=news&user=ana"))
	require.Equal(t, "event: news\nid: 7\ndata: for ana\n\ndata: \"{{ .Extra.id }}\"\n\n", string(body))
}

func TestReplyEventStream_CallbacksSeeTheWholeStream(t *testing.T) {
	// The mock counts the request and runs its callbacks once the stream ends, with the body it sent.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	seen := make(chan string, 1)
	mock := m.MustMock(Getf("/events").
		Reply(OK().EventStream(NewEventStream(NewEvent().Data("a"), NewEvent().Data("b")))).
		Callback(func(_ context.Context, in vlatte.OnComplete) error {
			seen <- string(in.(*OnComplete).Response().Body)
			return nil
		}))

	fetch(t, m.URL("/events"))
	require.Equal(t, "data: a\n\ndata: b\n\n", <-seen)
	require.Equal(t, int64(1), mock.Hits())
}

func TestReplyEventStream_BuildErrors(t *testing.T) {
	// What can't make an event stream fails the build of the mock, saying why.
	fn := func() {}
	_, errJSON := json.Marshal(fn)

	tests := []struct {
		name    string
		builder *Builder
		want    string
	}{
		{"a Content-Type header too", Getf("/x").Reply(OK().ContentType("text/plain").EventStream(NewEventStream())), "reply: an event stream sets the Content-Type: remove the Content-Type header"},
		{"a line break in an id", Getf("/x").Reply(OK().EventStream(NewEventStream(NewEvent().ID("1\n2")))), "reply: sse: events[0]: id holds a line break"},
		{"raw with fields", Getf("/x").Reply(OK().EventStream(NewEventStream(NewEvent().Raw("x").Data("y")))), "reply: sse: events[0]: raw is set with other fields: raw is sent alone, as it is"},
		{"JSON that doesn't marshal", Getf("/x").Reply(OK().EventStream(NewEventStream(NewEvent().DataJSON(fn)))), "reply: sse: events[0]: " + errJSON.Error()},
		{"response mappers", Getf("/x").Reply(OK().EventStream(NewEventStream())).Map(func(*RequestValues, *Response) error { return nil }), "mock: response mappers read the whole body, and an event stream doesn't end: remove the mappers"},
	}

	m := MustNew(Setup().Silent())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(tc.builder)
			require.EqualError(t, err, "server: mock 0: "+tc.want)
		})
	}
}

func TestReplyEventStream_MappersFailOnAStreamInASequence(t *testing.T) {
	// A sequence hides its replies from the build: the mappers fail at the request, rather than wait for a stream
	// that may not end.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").
		Reply(Seq(OK().EventStream(NewEventStream(NewEvent().Data("a")).KeepOpen()))).
		Map(func(*RequestValues, *Response) error { return nil }))

	res, body := fetch(t, m.URL("/events"))
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "response mappers read the whole body, and an event stream doesn't end")
}

func TestReplyEventStream_LastBodySetterWins(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/text").Reply(OK().EventStream(NewEventStream(NewEvent().Data("a"))).BodyText("text")))
	m.MustMock(Getf("/events").Reply(OK().BodyText("text").EventStream(NewEventStream(NewEvent().Data("a")))))

	res, body := fetch(t, m.URL("/text"))
	require.Equal(t, "text/plain; charset=utf-8", res.Header.Get(HeaderContentType), "net/http sniffs a Content-Type for the text")
	require.Equal(t, "text", string(body))

	_, body = fetch(t, m.URL("/events"))
	require.Equal(t, "data: a\n\n", string(body))
}

func TestNewEventStream_CopiesItsEvents(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	ev := NewEvent().Data("before")
	es := NewEventStream(ev)
	ev.Data("after")

	m.MustMock(Getf("/events").Reply(OK().EventStream(es)))

	_, body := fetch(t, m.URL("/events"))
	require.Equal(t, "data: before\n\n", string(body))
}

func TestReplyEventStream_ServesARequestWithoutARawRequest(t *testing.T) {
	// A reply made outside a server, as in a test of a replier, serves its stream to request values without the
	// request: it has no Last-Event-ID.
	r, err := OK().EventStream(NewEventStream(NewEvent().ID("1").Data("a")).ResumeFromLastEventID()).NewReplier(nil)
	require.NoError(t, err)

	res, err := r.Reply(t.Context(), ReplyInput{RequestValues: &RequestValues{}})
	require.NoError(t, err)

	body, err := io.ReadAll(res.BodyCloser)
	require.NoError(t, err)
	require.Equal(t, "id: 1\ndata: a\n\n", string(body))
}

func TestReplyEventStream_ATemplateThatFailsAtTheRequestFailsTheReply(t *testing.T) {
	// A templated id that renders a line break would end the field there: the reply fails, and says why, before it
	// sends anything.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/events").Reply(OK().EventStream(NewEventStream(
		NewEvent().ID(`{{ .Request.URL.Query.Get "id" }}`).Data("a"),
	).Template())))

	res, body := fetch(t, m.URL("/events?id=1%0A2"))
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "reply: sse: events[0]: id holds a line break")
}
