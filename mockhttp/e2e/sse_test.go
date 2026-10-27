package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// sseClient reads an event stream as EventSource does, event by event, and keeps the last event ID it saw.
type sseClient struct {
	r      *bufio.Reader
	lastID string
}

// next reads the next event, up to the blank line that ends it, and returns its lines. It returns the error that
// ended the stream before the event did.
func (c *sseClient) next() ([]string, error) {
	var lines []string

	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return lines, err
		}

		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return lines, nil
		}

		if id, ok := strings.CutPrefix(line, "id: "); ok {
			c.lastID = id
		}

		lines = append(lines, line)
	}
}

// openStream sends a GET of url with client and ctx, with lastEventID as its Last-Event-ID when it is set.
func openStream(ctx context.Context, t *testing.T, client *http.Client, url, lastEventID string) (*http.Response, *sseClient) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderAccept, mockhttp.MIMETextEventStream)

	if lastEventID != "" {
		req.Header.Set(mockhttp.HeaderLastEventID, lastEventID)
	}

	res, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, mockhttp.MIMETextEventStream, res.Header.Get(mockhttp.HeaderContentType))

	return res, &sseClient{r: bufio.NewReader(res.Body)}
}

func TestSSE_ReconnectsAfterADropAndResumes(t *testing.T) {
	// The connection drops after the first event. The client reconnects with the id of the last event it got, and the
	// stream resumes after it: nothing is lost, nothing sent twice.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	stream := mockhttp.NewEventStream(
		mockhttp.NewEvent().ID("1").Data("a"),
		mockhttp.NewEvent().ID("2").Data("b"),
		mockhttp.NewEvent().ID("3").Data("c"),
	).ResumeFromLastEventID()

	m.MustMock(mockhttp.Getf("/events").Once().
		Step(fault.LimitData(len("id: 1\ndata: a\n\n"))).
		Reply(mockhttp.OK().EventStream(stream)))
	m.MustMock(mockhttp.Getf("/events").
		Header(mockhttp.HeaderLastEventID, match.Equal("1")).
		Reply(mockhttp.OK().EventStream(stream)))

	client := newClient(t)

	_, first := openStream(t.Context(), t, client, m.URL("/events"), "")

	ev, err := first.next()
	require.NoError(t, err)
	require.Equal(t, []string{"id: 1", "data: a"}, ev)

	_, err = first.next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "the connection dropped")

	_, second := openStream(t.Context(), t, client, m.URL("/events"), first.lastID)

	var got [][]string
	for {
		ev, err := second.next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)
		got = append(got, ev)
	}

	require.Equal(t, [][]string{{"id: 2", "data: b"}, {"id: 3", "data: c"}}, got)
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
	require.Equal(t, []string{"data: hi"}, ev)
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
	require.Equal(t, []string{"event: tick", "data: 1"}, ev)
}
