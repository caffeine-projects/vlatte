package mockhttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step  = wireLogStep{}
	_ vlatte.Stage = (*wireLogStage)(nil)
	_ vlatte.Step  = holdHeadStep{}
	_ vlatte.Step  = seenStep{}
)

// dialServer opens a connection to the server of m, and sends it a GET of path in HTTP/1.1.
func dialServer(t *testing.T, m *Server, path string) net.Conn {
	t.Helper()

	u, err := url.Parse(m.URL(path))
	require.NoError(t, err)

	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+u.Host+"\r\n\r\n")
	require.NoError(t, err)

	return conn
}

// readWire reads what the server sends on conn until the connection ends, and the error that ended it: nil for an
// end.
func readWire(conn net.Conn) ([]byte, error) {
	return io.ReadAll(conn)
}

// wireLogStep sends what its stage passed on, as it ends.
type wireLogStep struct{ got chan<- []byte }

func (s wireLogStep) Open(_ *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &wireLogStage{got: s.got, next: next}
}

type wireLogStage struct {
	got  chan<- []byte
	seen []byte
	next vlatte.Stage
}

func (s *wireLogStage) Write(b []byte) (int, error) {
	s.seen = append(s.seen, b...)
	return s.next.Write(b)
}

func (s *wireLogStage) Flush() error { return s.next.Flush() }

func (s *wireLogStage) Close() error {
	s.got <- s.seen
	return s.next.Close()
}

func TestWire_AStageSeesEveryByteOfTheAnswer(t *testing.T) {
	// A fault on the wire changes what isn't the body: the status line, the headers and the framing. The steps see
	// what the client reads, an HTTP/1.1 answer that ends with the connection.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	got := make(chan []byte, 1)
	scoped := m.MustMock(Getf("/wire").Step(appendStep("!")).WireStep(wireLogStep{got: got}).Reply(OK().PlainText("hello")))

	wire, err := readWire(dialServer(t, m, "/wire"))
	require.NoError(t, err)
	require.Equal(t, wire, <-got)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	// The body steps come first: the wire sees the body they send.
	require.Equal(t, "hello!", string(body))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "HTTP/1.1", res.Proto)
	require.Equal(t, []string{"chunked"}, res.TransferEncoding)
	require.True(t, res.Close)
	require.Equal(t, MIMETextPlain, res.Header.Get(HeaderContentType))
	require.True(t, bytes.HasSuffix(wire, []byte("6\r\nhello!\r\n0\r\n\r\n")), "%q", wire)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestWire_AnAbortClosesTheConnectionThere(t *testing.T) {
	// A stage that passes the head on, then aborts, leaves the client with a valid head and a body that breaks off:
	// the mock counts the call, as for any answer.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	headOnly := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		if bytes.HasPrefix(b, []byte("HTTP/1.1 ")) {
			return next.Write(b)
		}

		return 0, vlatte.ErrAbort
	})
	scoped := m.MustMock(Getf("/cut").WireStep(headOnly).Reply(OK().PlainText("hello")))

	wire, err := readWire(dialServer(t, m, "/cut"))
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	_, err = io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestWire_AResetResetsTheConnection(t *testing.T) {
	// A peer that resets the connection is an error a client must handle apart from a connection that ends.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	reset := vlatte.StepFunc(func(context.Context, []byte, vlatte.Stage) (int, error) { return 0, vlatte.ErrReset })
	scoped := m.MustMock(Getf("/reset").WireStep(reset).Reply(OK().PlainText("hello")))

	wire, err := readWire(dialServer(t, m, "/reset"))
	require.ErrorIs(t, err, syscall.ECONNRESET)
	require.Empty(t, wire)
	require.EqualValues(t, 1, scoped.Hits())
}

// holdHeadStep holds the first bytes of the answer, the status line and the headers, until release is closed. It
// tells held once it holds them.
type holdHeadStep struct {
	held    chan<- struct{}
	release <-chan struct{}
}

func (s holdHeadStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	first := true

	return vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		if first {
			first = false
			close(s.held)
			<-s.release
		}

		return next.Write(b)
	}).Open(flow, next)
}

func TestWire_AStageCanHoldTheStatusLine(t *testing.T) {
	// A server slow to answer at all, whose client gets no byte until the head comes, as a timeout on the first byte
	// tests.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	held := make(chan struct{})
	release := make(chan struct{})
	m.MustMock(Getf("/slow").WireStep(holdHeadStep{held: held, release: release}).Reply(OK().PlainText("hello")))

	conn := dialServer(t, m, "/slow")
	<-held

	// Nothing is on the wire yet: a read that can't wait finds nothing.
	require.NoError(t, conn.SetReadDeadline(time.Now()))
	n, err := conn.Read(make([]byte, 1))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.Zero(t, n)

	close(release)
	require.NoError(t, conn.SetReadDeadline(time.Time{}))

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
}

func TestWire_AFailureIsVlattesAnswerWithoutTheSteps(t *testing.T) {
	// A failure is vlatte's answer, not the mock's: a step that drops everything doesn't hide it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	drop := vlatte.StepFunc(func(_ context.Context, b []byte, _ vlatte.Stage) (int, error) { return len(b), nil })
	m.MustMock(Getf("/fail").WireStep(drop).Reply(OK().BodyFile("missing.txt")))

	wire, err := readWire(dialServer(t, m, "/fail"))
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)

	_, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.True(t, res.Close)
}

func TestWire_APanicIsVlattesAnswerWithoutTheSteps(t *testing.T) {
	// A panic is a failure, which the client learns from vlatte's answer on the connection the mock took over, as on
	// any other: a step that drops everything doesn't hide it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	drop := vlatte.StepFunc(func(_ context.Context, b []byte, _ vlatte.Stage) (int, error) { return len(b), nil })
	m.MustMock(Getf("/panic").WireStep(drop).Reply(Handler(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))

	wire, err := readWire(dialServer(t, m, "/panic"))
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailurePanic, res.Header.Get(HeaderVlatteError))
	require.Equal(t, "PANIC\nGET /panic\n\nboom", string(body))
	require.True(t, res.Close)
}

func TestWire_AHandlerThatAbortsCutsTheConnection(t *testing.T) {
	// net/http aborts a response with http.ErrAbortHandler, as a reverse proxy does once its target breaks off: the
	// client keeps what was sent, then reads a body that breaks off, not a panic answer after it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/abort").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "part")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})))

	res, err := http.ReadResponse(bufio.NewReader(dialServer(t, m, "/abort")), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "part", string(body))
}

func TestWire_ABodyThatFailsIsVlattesAnswer(t *testing.T) {
	// The mock reads what is left of the body before it takes its connection over: a body that fails to read, such as
	// a malformed chunk, fails the reply, which the client learns from vlatte's answer.
	m := MustNew(Setup().Silent().NoBodyParsing())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/body").WireStep(vlatte.StepFunc(passWire)).Reply(OK()))

	u, err := url.Parse(m.URL("/body"))
	require.NoError(t, err)

	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "POST /body HTTP/1.1\r\nHost: "+u.Host+"\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n")
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "http: reading the request body: invalid byte in chunk length")
	require.EqualValues(t, 0, scoped.Hits())
}

func TestWire_BehindAWriterThatCantHandTheConnectionOverIsVlattesAnswer(t *testing.T) {
	// A middleware's writer that doesn't unwrap to the server's can't give the connection over: the client learns
	// why, rather than an answer without the steps.
	m := MustNew(Setup().Silent().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(opaqueWriter{w}, r) })
	}))
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/wire").WireStep(vlatte.StepFunc(passWire)).Reply(OK().PlainText("hello")))

	res, err := http.ReadResponse(bufio.NewReader(dialServer(t, m, "/wire")), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), errWireNeedsHTTP1.Error())
	require.EqualValues(t, 0, scoped.Hits())
}

func TestWire_WhileTheServerClosesNothingIsAnswered(t *testing.T) {
	// A connection taken over while the server closes is one Close no longer ends: it is closed instead, without an
	// answer.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/wire").WireStep(vlatte.StepFunc(passWire)).Reply(OK().PlainText("hello")))

	// What Close does once the server stops taking requests.
	m.tunnels.closeAll()

	wire, err := readWire(dialServer(t, m, "/wire"))
	require.NoError(t, err)
	require.Empty(t, wire)
	require.EqualValues(t, 0, scoped.Hits())
}

func TestWire_OverHTTP2IsVlattesAnswer(t *testing.T) {
	// HTTP/2 can't give a connection over, so a mock with wire steps can't answer on it: the client learns why, rather
	// than an answer without the steps.
	m := MustNew(Setup().Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/h2").WireStep(wireLogStep{got: make(chan []byte, 1)}).Reply(OK().PlainText("hello")))

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, Protocols: new(http.Protocols)}
	tr.Protocols.SetHTTP2(true)
	t.Cleanup(tr.CloseIdleConnections)

	res, err := (&http.Client{Transport: tr}).Get(m.URL("/h2"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, 2, res.ProtoMajor)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), errWireNeedsHTTP1.Error())
	require.EqualValues(t, 0, scoped.Hits())
}

func TestWire_TheEndOfTheAnswerCarriesTheTrailers(t *testing.T) {
	// The answer the server writes itself on the wire still ends as HTTP/1.1 does: the last chunk, then the trailers
	// the reply set once its body was written.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/trailer").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Sum")
		_, _ = io.WriteString(w, "body")
		w.Header().Set("X-Sum", "4")
		w.Header().Set(http.TrailerPrefix+"X-Late", "yes")
	})))

	conn := dialServer(t, m, "/trailer")

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "body", string(body))
	require.Equal(t, "4", res.Trailer.Get("X-Sum"))
	require.Equal(t, "yes", res.Trailer.Get("X-Late"))
}

func TestWire_ATrailerSetBeforeTheBodyStaysOutOfTheHead(t *testing.T) {
	// A header with http.TrailerPrefix is a trailer whenever the reply sets it, as net/http sends it: the head
	// doesn't carry it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/trailer").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(http.TrailerPrefix+"X-Early", "yes")
		_, _ = io.WriteString(w, "body")
	})))

	wire, err := readWire(dialServer(t, m, "/trailer"))
	require.NoError(t, err)

	head, _, _ := bytes.Cut(wire, []byte("\r\n\r\n"))
	require.NotContains(t, string(head), "X-Early")

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "body", string(body))
	require.Equal(t, http.Header{"X-Early": {"yes"}}, res.Trailer)
}

func TestWire_AnEmptyWriteDoesntEndTheBody(t *testing.T) {
	// An empty chunk ends a chunked body: an empty write, such as of a stream that had nothing yet, sends none.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/empty").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "bo")
		_, _ = w.Write(nil)
		_, _ = io.WriteString(w, "dy")
	})))

	res, err := http.ReadResponse(bufio.NewReader(dialServer(t, m, "/empty")), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "body", string(body))
}

func TestWire_AReplyThatWritesNothingAnswers200(t *testing.T) {
	// As net/http does, a reply that writes nothing answers 200 with an empty body, which still ends as HTTP/1.1
	// does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/nothing").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(http.ResponseWriter, *http.Request) {})))

	wire, err := readWire(dialServer(t, m, "/nothing"))
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, body)
	require.True(t, bytes.HasSuffix(wire, []byte("\r\n\r\n0\r\n\r\n")), "%q", wire)
	require.EqualValues(t, 1, scoped.Hits())
}

func passWire(_ context.Context, b []byte, next vlatte.Stage) (int, error) { return next.Write(b) }

func TestWire_AnHTTP10ClientGetsAnAnswerThatEndsWithTheConnection(t *testing.T) {
	// HTTP/1.0 has no chunks: the body ends where the connection does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/old").WireStep(vlatte.StepFunc(passWire)).Reply(OK().PlainText("hello")))

	u, err := url.Parse(m.URL("/old"))
	require.NoError(t, err)

	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET /old HTTP/1.0\r\nHost: "+u.Host+"\r\n\r\n")
	require.NoError(t, err)

	wire, err := readWire(conn)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(wire, []byte("HTTP/1.0 200 OK\r\n")), "%q", wire)
	require.True(t, bytes.HasSuffix(wire, []byte("\r\n\r\nhello")), "%q", wire)
}

func TestWire_AHeadAnswerHasNoBody(t *testing.T) {
	// A HEAD's answer has headers only: the body the reply writes doesn't go out, nor its framing.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	got := make(chan []byte, 1)
	m.MustMock(Headf("/head").WireStep(wireLogStep{got: got}).Reply(OK().PlainText("hello")))

	u, err := url.Parse(m.URL("/head"))
	require.NoError(t, err)

	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "HEAD /head HTTP/1.1\r\nHost: "+u.Host+"\r\n\r\n")
	require.NoError(t, err)

	wire, err := readWire(conn)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(wire, []byte("\r\n\r\n")), "%q", wire)
	require.NotContains(t, string(wire), "hello")
	require.NotContains(t, string(wire), "Transfer-Encoding")
	require.Equal(t, wire, <-got)
}

func TestWire_AnInformationalStatusComesFirst(t *testing.T) {
	// An early hint goes out ahead of the answer, as net/http sends it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/hint").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusTeapot)
	})))

	wire, err := readWire(dialServer(t, m, "/hint"))
	require.NoError(t, err)

	br := bufio.NewReader(bytes.NewReader(wire))

	hint, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusEarlyHints, hint.StatusCode)
	require.Equal(t, "</style.css>; rel=preload", hint.Header.Get("Link"))

	// The second final status is ignored, as net/http does.
	res, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, res.StatusCode)
}

func TestWire_ANoContentAnswerRefusesABody(t *testing.T) {
	// A status without a body refuses one, as net/http does: the reply learns it, and the client gets no framing.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	written := make(chan error, 1)

	m.MustMock(Getf("/none").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		_, err := io.WriteString(w, "body")
		written <- err
		_, _ = w.Write(nil)
		http.NewResponseController(w).Flush()
	})))

	wire, err := readWire(dialServer(t, m, "/none"))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(wire, []byte("HTTP/1.1 204 No Content\r\n")), "%q", wire)
	require.True(t, bytes.HasSuffix(wire, []byte("\r\n\r\n")), "%q", wire)
	require.NotContains(t, string(wire), "body")
	require.ErrorIs(t, <-written, http.ErrBodyNotAllowed)
}

func TestWire_AnUnknownStatusHasAText(t *testing.T) {
	// A status net/http has no text for still gets a status line a client can read.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/odd").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(599)
	})))

	wire, err := readWire(dialServer(t, m, "/odd"))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(wire, []byte("HTTP/1.1 599 status code 599\r\n")), "%q", wire)
}

func TestWire_TheRequestBodyCanStillBeRead(t *testing.T) {
	// net/http forbids the body once the connection is taken over: what is left of it is read first, so a reply, such
	// as Echo, still reads it.
	m := MustNew(Setup().Silent().NoBodyParsing())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/body").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			b = []byte(err.Error())
		}

		_, _ = w.Write(b)
	})))

	res, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Post(m.URL("/body"), MIMETextPlain, bytes.NewBufferString("ping"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "ping", string(body))
}

// seenStep sends each part its stage gets on seen, then passes it on.
type seenStep struct{ seen chan<- string }

func (s seenStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		s.seen <- string(b)
		return next.Write(b)
	}).Open(flow, next)
}

func TestWire_UpstreamStepsTakeABodyLeftUnreadAsItComes(t *testing.T) {
	// The mock takes its connection over before it replies, so it reads first what the server left of the body,
	// through the upstream steps: they take it from the client as it comes, so a stage that holds a part holds the
	// upload. The target gets the body as they passed it on, once.
	got := make(chan upstreamReceived, 1)
	target := receivingTarget(t, got)

	m := MustNew(Setup().Silent().NoBodyParsing())
	m.MustStart()
	t.Cleanup(m.Close)

	seen := make(chan string, 4)
	m.MustMock(Postf("/up").
		WireStep(vlatte.StepFunc(passWire)).
		UpstreamStep(seenStep{seen: seen}, appendStep("?")).
		Reply(From(target.URL)))

	upload, sending := io.Pipe()
	answered := make(chan error, 1)

	go func() {
		res, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Post(m.URL("/up"), MIMETextPlain, upload)
		if err == nil {
			_ = res.Body.Close()
		}

		answered <- err
	}()

	_, err := io.WriteString(sending, "part1")
	require.NoError(t, err)

	// The guard only bounds a failure.
	select {
	case part := <-seen:
		require.Equal(t, "part1", part)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the steps waited for the whole upload")
	}

	_, err = io.WriteString(sending, "part2")
	require.NoError(t, err)
	require.NoError(t, sending.Close())

	require.NoError(t, <-answered)
	require.Equal(t, upstreamReceived{body: "part1?part2?", contentLength: -1, transferEncoding: []string{"chunked"}}, <-got)
}

func TestWire_AnUpstreamAbortOfABodyLeftUnreadCutsTheAnswer(t *testing.T) {
	// A stage that cuts the upload as the mock reads it cuts the answer before it starts: the cut is the mock's
	// answer, so the call counts, and the target gets nothing.
	got := make(chan upstreamReceived, 1)
	target := receivingTarget(t, got)

	m := MustNew(Setup().Silent().NoBodyParsing())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/up").
		WireStep(vlatte.StepFunc(passWire)).
		UpstreamStep(limitStep{n: 2, err: vlatte.ErrAbort}).
		Reply(From(target.URL)))

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	_, err := client.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.ErrorIs(t, err, io.EOF)
	require.EqualValues(t, 1, scoped.Hits())
	require.Empty(t, got)
}

func TestWire_AnAbortOfTheBodyStepsCutsTheWireToo(t *testing.T) {
	// A body step that cuts the answer cuts the connection the wire steps answer on.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/both").
		Step(limitStep{n: 2, err: vlatte.ErrReset}).
		WireStep(vlatte.StepFunc(passWire)).
		Reply(OK().PlainText("hello")))

	_, err := readWire(dialServer(t, m, "/both"))
	require.ErrorIs(t, err, syscall.ECONNRESET)
}

func TestWire_AFlushSendsTheHeadAtOnce(t *testing.T) {
	// A stream sends its head before its first event: the client reads it while the reply waits.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	release := make(chan struct{})
	m.MustMock(Getf("/stream").WireStep(vlatte.StepFunc(passWire)).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "event")
	})))

	conn := dialServer(t, m, "/stream")
	br := bufio.NewReader(conn)

	res, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	close(release)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "event", string(body))
}

func TestWire_ACutOfTheBodyStepsEndsTheAnswerShort(t *testing.T) {
	// A body step that cuts the answer leaves the client with a body that breaks off, not one that looks whole: the
	// wire doesn't get its last chunk.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/cut").
		Step(limitStep{n: 2, err: vlatte.ErrAbort}).
		WireStep(vlatte.StepFunc(passWire)).
		Reply(OK().PlainText("hello")))

	res, err := http.ReadResponse(bufio.NewReader(dialServer(t, m, "/cut")), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "he", string(body))
	require.EqualValues(t, 1, scoped.Hits())
}
