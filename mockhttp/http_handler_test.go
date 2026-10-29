package mockhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

// appendStep appends its text to every buffer it passes on.
type appendStep string

func (s appendStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return vlatte.StepFunc(func(_ context.Context, p []byte, next vlatte.Stage) (int, error) {
		if _, err := next.Write(append(slices.Clip(p), s...)); err != nil {
			return 0, err
		}

		return len(p), nil
	}).Open(flow, next)
}

func TestTopResponseWriter_EveryWritePassesTheSteps(t *testing.T) {
	// D1: a pipeline passes one buffer once, and the writer reused one for every write, so the 2nd write returned
	// io.EOF. Write reports len(p) as well: io.Copy rejects a count larger than the buffer it handed over.
	rec := httptest.NewRecorder()
	w := newTopResponseWriter(t.Context(), []vlatte.Step{appendStep("!")}, rec)

	for _, s := range []string{"a", "b", "c"} {
		n, err := w.Write([]byte(s))
		require.NoError(t, err)
		require.Equal(t, len(s), n)
	}

	require.Equal(t, "a!b!c!", rec.Body.String())
}

func TestTopResponseWriter_AStepCanPassAWriteOnInParts(t *testing.T) {
	// fault.Slicer passes each write on in parts. Every part after the first failed the write, which cut the body.
	slicer := vlatte.StepFunc(func(_ context.Context, p []byte, next vlatte.Stage) (int, error) {
		for part := range slices.Chunk(p, 2) {
			if _, err := next.Write(part); err != nil {
				return 0, err
			}
		}

		return len(p), nil
	})

	rec := httptest.NewRecorder()
	w := newTopResponseWriter(t.Context(), []vlatte.Step{slicer}, rec)

	n, err := w.Write([]byte("hello"))

	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "hello", rec.Body.String())
}

func TestTopResponseWriter_FlushReachesTheResponseWriter(t *testing.T) {
	// D2: streamed parts are flushed with http.ResponseController, which reaches the response writer through the steps.
	rec := httptest.NewRecorder()
	w := newTopResponseWriter(t.Context(), []vlatte.Step{appendStep("")}, rec)

	require.NoError(t, http.NewResponseController(w).Flush())
	require.True(t, rec.Flushed)
}

func TestSteps_ServeABodyFileLargerThanOneWrite(t *testing.T) {
	// D1: io.Copy writes a body file in 32 KB chunks, and through steps every chunk after the first failed.
	body := bytes.Repeat([]byte("0123456789abcdef"), 8<<10)
	file := filepath.Join(t.TempDir(), "body.bin")
	require.NoError(t, os.WriteFile(file, body, 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	// The reply's headers reach the client through the steps too.
	m.MustMock(Getf("/file").Step(appendStep("")).Reply(OK().Header("X-Id", "7").BodyFile(file)))

	res, err := http.Get(m.URL("/file"))
	require.NoError(t, err)

	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "7", res.Header.Get("X-Id"))
	require.Equal(t, body, got)
}

// failingStep is a step that fails every write.
type failingStep struct{}

func (failingStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return vlatte.StepFunc(func(context.Context, []byte, vlatte.Stage) (int, error) {
		return 0, errors.New("step: failed")
	}).Open(flow, next)
}

func TestTopResponseWriter_AFailingStepFailsTheWrite(t *testing.T) {
	// A write a step fails writes nothing, and says why: io.Copy and Render stop there.
	rec := httptest.NewRecorder()
	w := newTopResponseWriter(t.Context(), []vlatte.Step{failingStep{}}, rec)

	n, err := w.Write([]byte("a"))
	require.EqualError(t, err, "step: failed")
	require.Zero(t, n)
	require.Empty(t, rec.Body.String())
}

var (
	_ vlatte.Step  = asyncStep{}
	_ vlatte.Stage = (*asyncStage)(nil)
	_ vlatte.Step  = logStep{}
	_ vlatte.Stage = (*logStage)(nil)
	_ vlatte.Step  = limitStep{}
	_ vlatte.Stage = (*limitStage)(nil)
	_ vlatte.Step  = closeStep{}
	_ vlatte.Stage = (*closeStage)(nil)
	_ vlatte.Step  = abortOn{}
)

// asyncStep passes on what its stage gets from a goroutine of its own, in order, as a stage that delays a stream
// does: the reply and the stage write to the response at once.
type asyncStep struct{}

func (asyncStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	s := &asyncStage{ctx: flow.Context(), next: next, ops: make(chan asyncOp), done: make(chan error, 1)}
	go s.run()

	return s
}

// asyncOp is a write, or a flush that answers on flushed.
type asyncOp struct {
	b       []byte
	flushed chan error
}

type asyncStage struct {
	ctx  context.Context
	next vlatte.Stage
	ops  chan asyncOp
	done chan error
}

func (s *asyncStage) Write(b []byte) (int, error) {
	select {
	case s.ops <- asyncOp{b: bytes.Clone(b)}:
		return len(b), nil
	case <-s.ctx.Done():
		return 0, context.Cause(s.ctx)
	}
}

func (s *asyncStage) Flush() error {
	flushed := make(chan error, 1)

	select {
	case s.ops <- asyncOp{flushed: flushed}:
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	}

	select {
	case err := <-flushed:
		return err
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	}
}

func (s *asyncStage) Close() error {
	close(s.ops)

	if err := <-s.done; err != nil {
		return err
	}

	return s.next.Close()
}

func (s *asyncStage) run() {
	var err error
	defer func() { s.done <- err }()

	for {
		select {
		case op, ok := <-s.ops:
			switch {
			case !ok:
				return
			case err != nil:
			case op.flushed != nil:
				op.flushed <- s.next.Flush()
			default:
				_, err = s.next.Write(op.b)
			}
		case <-s.ctx.Done():
			err = context.Cause(s.ctx)
			return
		}
	}
}

// logStep logs each call its stage gets, before it passes the call on.
type logStep struct{ log *[]string }

func (s logStep) Open(_ *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &logStage{log: s.log, next: next}
}

type logStage struct {
	log  *[]string
	next vlatte.Stage
}

func (s *logStage) Write(b []byte) (int, error) {
	*s.log = append(*s.log, "write "+string(b))
	return s.next.Write(b)
}

func (s *logStage) Flush() error {
	*s.log = append(*s.log, "flush")
	return s.next.Flush()
}

func (s *logStage) Close() error {
	*s.log = append(*s.log, "close")
	return s.next.Close()
}

func TestTopResponseWriter_AFlushGoesThroughTheSteps(t *testing.T) {
	// A stage that delays a stream must delay its flushes too: a flush that got past it would send the headers, and
	// what came before it, early.
	var log []string
	rec := httptest.NewRecorder()
	w := newTopResponseWriter(t.Context(), []vlatte.Step{logStep{log: &log}}, rec)

	require.NoError(t, http.NewResponseController(w).Flush())

	var flusher http.Flusher = w
	flusher.Flush()

	require.Equal(t, []string{"flush", "flush"}, log)
	require.True(t, rec.Flushed)
}

// timedWriter is a response writer that logs each write and flush with the time it came, from start.
type timedWriter struct {
	header http.Header
	start  time.Time
	log    []string
}

func (w *timedWriter) Header() http.Header { return w.header }

func (w *timedWriter) WriteHeader(int) {}

func (w *timedWriter) Write(p []byte) (int, error) {
	w.log = append(w.log, string(p)+" at "+time.Since(w.start).String())
	return len(p), nil
}

func (w *timedWriter) Flush() {
	w.log = append(w.log, "flush at "+time.Since(w.start).String())
}

func TestTopResponseWriter_AStepSendsItsPartsWhenItWants(t *testing.T) {
	// A body sent in timed parts, flushed one by one, reaches the client part by part: the steps time what goes out.
	synctest.Test(t, func(t *testing.T) {
		dribble := vlatte.StepFunc(func(_ context.Context, p []byte, next vlatte.Stage) (int, error) {
			for part := range slices.Chunk(p, 2) {
				time.Sleep(100 * time.Millisecond)

				if _, err := next.Write(part); err != nil {
					return 0, err
				}

				if err := next.Flush(); err != nil {
					return 0, err
				}
			}

			return len(p), nil
		})

		tw := &timedWriter{header: http.Header{}, start: time.Now()}
		w := newTopResponseWriter(t.Context(), []vlatte.Step{dribble}, tw)

		_, err := w.Write([]byte("abcd"))
		require.NoError(t, err)
		require.NoError(t, w.end())

		require.Equal(t, []string{"ab at 100ms", "flush at 100ms", "cd at 200ms", "flush at 200ms"}, tw.log)
	})
}

func TestSteps_AStageCanWriteFromAGoroutineOfItsOwn(t *testing.T) {
	// A stage that delays a stream writes from its own goroutine while the reply goes on with the response: setting a
	// trailer, flushing, setting a deadline. The race detector checks they don't race.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	controls := make(chan [3]error, 1)

	m.MustMock(Getf("/async").Step(asyncStep{}, appendStep("!")).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Sum")
		_, _ = io.WriteString(w, "a")
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "b")

		controls <- [3]error{
			http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute)),
			http.NewResponseController(w).SetReadDeadline(time.Now().Add(time.Minute)),
			http.NewResponseController(w).EnableFullDuplex(),
		}

		w.Header().Set("X-Sum", "2")
	})))

	res, err := http.Get(m.URL("/async"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, "a!b!", string(body))
	require.Equal(t, "2", res.Trailer.Get("X-Sum"))
	require.Equal(t, [3]error{nil, nil, nil}, <-controls)
}

func TestSteps_AHandlerCanTakeItsConnectionOver(t *testing.T) {
	// A handler that answers on the connection itself, as one that switches protocols does, still can behind steps:
	// what it writes there is past them, which see only what it writes to the response.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/raw").Step(appendStep("!")).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		defer conn.Close()

		_, _ = io.WriteString(rw, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi")
		_ = rw.Flush()
	})))

	res, err := http.Get(m.URL("/raw"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hi", string(body))
}

func TestSteps_AStepThatFailsAsTheAnswerEndsIsLogged(t *testing.T) {
	// The steps end once a reply wrote its answer, which is sent by then: a step that fails there can't change it, so
	// the failure is logged, and the mock counts as served.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	failed := errors.New("step: failed at the end")
	scoped := m.MustMock(Getf("/end").Step(closeStep{closed: make(chan error, 1), err: failed}).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "body")
	})))

	res, err := http.Get(m.URL("/end"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "body", string(body))
	require.EqualValues(t, 1, scoped.Hits())
	require.Contains(t, logs.String(), "http: ending the steps: step: failed at the end")
}

// limitStep passes on the first n bytes of each response, then aborts it with err, as a limit on the data a
// connection takes does. Each response has its own count.
type limitStep struct {
	n   int
	err error
}

func (s limitStep) Open(_ *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &limitStage{left: s.n, err: s.err, next: next}
}

type limitStage struct {
	left int
	err  error
	next vlatte.Stage
}

func (s *limitStage) Write(b []byte) (int, error) {
	if len(b) <= s.left {
		s.left -= len(b)
		return s.next.Write(b)
	}

	if _, err := s.next.Write(b[:s.left]); err != nil {
		return 0, err
	}

	n := s.left
	s.left = 0

	return n, s.err
}

func (s *limitStage) Flush() error { return s.next.Flush() }

func (s *limitStage) Close() error { return s.next.Close() }

func TestSteps_AStepCanCutEachResponseAfterSomeBytes(t *testing.T) {
	// The count of a limit is the response's own, though the mock's steps serve all its requests: each client gets
	// the first bytes, then a cut, not a short body that looks whole. The cut is the mock's answer: it counts.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/limit").Step(limitStep{n: 5, err: vlatte.ErrAbort}).Reply(OK().PlainText("hello world")))

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			res, err := http.Get(m.URL("/limit"))
			if !assert.NoError(t, err) {
				return
			}

			body, err := io.ReadAll(res.Body)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
			assert.Equal(t, "hello", string(body))
			assert.NoError(t, res.Body.Close())
		})
	}

	wg.Wait()

	require.EqualValues(t, 2, scoped.Hits())
}

func TestSteps_AResetCutsTheConnectionWithAReset(t *testing.T) {
	// A peer that resets the connection is an error a client must handle apart from a connection that ends.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/reset").Step(limitStep{n: 5, err: vlatte.ErrReset}).Reply(OK().PlainText("hello world")))

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	res, err := client.Get(m.URL("/reset"))
	if err == nil {
		_, err = io.ReadAll(res.Body)
		_ = res.Body.Close()
	}

	require.ErrorIs(t, err, syscall.ECONNRESET)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestSteps_AResetCutsATLSConnectionWithAReset(t *testing.T) {
	// A reset reaches the TCP connection under TLS too: the client reads a reset, not the end of the TLS stream.
	m := MustNew(Setup().Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/reset").Step(limitStep{n: 5, err: vlatte.ErrReset}).Reply(OK().PlainText("hello world")))

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true, Protocols: new(http.Protocols)}
	tr.Protocols.SetHTTP1(true)

	res, err := (&http.Client{Transport: tr}).Get(m.URL("/reset"))
	if err == nil {
		_, err = io.ReadAll(res.Body)
		_ = res.Body.Close()
	}

	require.ErrorIs(t, err, syscall.ECONNRESET)
	require.EqualValues(t, 1, scoped.Hits())
}

// closeStep sends, as its stage closes, the cause of its flow's context, nil while the flow is on.
type closeStep struct {
	closed chan<- error
	err    error
}

func (s closeStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &closeStage{flow: flow, closed: s.closed, err: s.err, next: next}
}

type closeStage struct {
	flow   *vlatte.Flow
	closed chan<- error
	err    error
	next   vlatte.Stage
}

func (s *closeStage) Write(b []byte) (int, error) { return s.next.Write(b) }

func (s *closeStage) Flush() error { return s.next.Flush() }

// Close fails with err, if any, as a stage that cuts a response at its end does.
func (s *closeStage) Close() error {
	s.closed <- context.Cause(s.flow.Context())

	if s.err != nil {
		return s.err
	}

	return s.next.Close()
}

func TestSteps_EveryResponseEndsItsSteps(t *testing.T) {
	// The end of a response is an event of the steps, whatever the reply: an empty body has no write, and a reply that
	// panics is vlatte's failure, which skips the steps. Either way, the stages end before the client gets the
	// answer, so their goroutines and timers don't outlive it.
	tests := []struct {
		name   string
		reply  ReplierFactory
		status int
		body   string
		abort  bool
	}{
		{name: "an empty body", reply: OK(), status: http.StatusOK},
		{
			name: "a reply that panics",
			reply: Handler(func(http.ResponseWriter, *http.Request) {
				panic("boom")
			}),
			status: StatusNoMatch,
			body:   "PANIC\nGET /end\n\nboom",
			abort:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			closed := make(chan error, 1)
			m.MustMock(Getf("/end").Step(closeStep{closed: closed}, asyncStep{}).Reply(tc.reply))

			res, err := http.Get(m.URL("/end"))
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
			require.Equal(t, tc.body, string(body))

			select {
			case cause := <-closed:
				require.Equal(t, tc.abort, errors.Is(cause, vlatte.ErrAbort))
			default:
				require.Fail(t, "the steps didn't end before the answer")
			}
		})
	}
}

func TestSteps_AStepCanCutAResponseWithoutABody(t *testing.T) {
	// A reply with no body writes nothing, so only the end of the response lets a step cut it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	closed := make(chan error, 1)
	scoped := m.MustMock(Getf("/empty").Step(closeStep{closed: closed, err: vlatte.ErrAbort}).Reply(OK()))

	res, err := http.Get(m.URL("/empty"))
	require.NoError(t, err)

	_, err = io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.EqualValues(t, 1, scoped.Hits())
}

// abortOn aborts its flow once release is closed, as a timer would, with the cause err.
type abortOn struct {
	release <-chan struct{}
	err     error
}

func (s abortOn) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	go func() {
		select {
		case <-s.release:
			flow.Abort(s.err)
		case <-flow.Context().Done():
		}
	}()

	return next
}

func TestSteps_AStepCanEndAReplyThatWaits(t *testing.T) {
	// A target that holds the rest of its body writes nothing: a step that times the response out aborts the flow,
	// and the reply waiting on the target ends with it. The client gets a cut, and the mock counts the call.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "start")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	errTimeout := errors.New("step: timed out")
	release := make(chan struct{})
	scoped := m.MustMock(Getf("/wait").Step(abortOn{release: release, err: errTimeout}).Reply(From(target.URL)))

	res, err := http.Get(m.URL("/wait"))
	require.NoError(t, err)

	first := make([]byte, len("start"))
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err)
	require.Equal(t, "start", string(first))

	close(release)

	rest, err := io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Empty(t, rest)
	require.NoError(t, res.Body.Close())
	require.EqualValues(t, 1, scoped.Hits())
}

func TestSteps_ConcurrentRequestsKeepTheirOwnResponse(t *testing.T) {
	// D1: three Step calls leave spare capacity in the mock's steps, and every request appended its sink there, so
	// concurrent requests could write into each other's responses. The race detector also flags it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/echo").
		Step(appendStep("1")).
		Step(appendStep("2")).
		Step(appendStep("3")).
		Reply(OK().BodyTemplate(`{{ .Request.URL.Query.Get "n" }}`)))

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			n := strconv.Itoa(i)

			res, err := http.Get(m.URL("/echo?n=" + n))
			if !assert.NoError(t, err) {
				return
			}

			b, err := io.ReadAll(res.Body)
			assert.NoError(t, err)
			assert.NoError(t, res.Body.Close())
			assert.Equal(t, n+"123", string(b))
		})
	}

	wg.Wait()
}

func TestHandler_RenderErrorsAreLoggedNotWritten(t *testing.T) {
	// D7: a body that fails while it is written already sent its status and headers. onError wrote a second status
	// and appended its error text to that body; now the error is logged and the mock counts as served.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	// A directory opens, then fails to read.
	scoped := m.MustMock(Getf("/dir").Reply(OK().BodyFile(t.TempDir())))

	res, err := http.Get(m.URL("/dir"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, b, "no error text in a body that already started")
	require.True(t, scoped.Called())
	require.Contains(t, logs.String(), "http: rendering response: ")
}

func TestHandler_AClientLeavingAStreamIsAWarning(t *testing.T) {
	// Decision 14: closing an x-mixed-replace stream is how a client stops it. The stream ends with a warning, not
	// an error, and the mock counts as served.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/stream").Reply(OK().Multipart(NewMultipart("x-mixed-replace",
		NewPart().BodyText("first"),
		NewPart().BodyText("never sent").Delay(time.Hour),
	).Boundary("b"))))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/stream"), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	// The stream sends the first part and the delimiter line of the second, then waits.
	first := make([]byte, len("--b\r\n\r\nfirst\r\n--b\r\n"))
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err)

	cancel()
	_ = res.Body.Close()

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), `"level":"warn","error":"http: rendering response: context canceled"`)
	}, 5*time.Second, 10*time.Millisecond, "logs: %s", logs)
	require.NotContains(t, logs.String(), `"level":"error"`)
	require.True(t, scoped.Called())
}

func TestHandler_FailuresAreToldApartFromNoMatch(t *testing.T) {
	// Issue #17: a mock that matched and failed answered as if no mock matched, with a body blaming matching.
	// Both keep the 418, so neither looks like a mock's answer, and VLatte-Error says which one it was.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").ID("file-mock").Name("missing file").SetSource("mocks/x.yaml").
		Reply(OK().BodyFile("missing.txt")))

	status, header, body := getFull(t, m, "/x")
	require.Equal(t, StatusNoMatch, status, body)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.True(t, strings.HasPrefix(body, "REPLY FAILED\nGET "+m.URL("/x")+"\n"), body)
	require.Contains(t, body, "\nMock [file-mock] missing file\nSource: mocks/x.yaml\n", "the body names the mock")
	require.Contains(t, body, "missing.txt", "and the error")

	status, header, body = getFull(t, m, "/nothing")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
	require.True(t, strings.HasPrefix(body, "REQUEST DID NOT MATCH\n"), body)
}

func TestHandler_ErrorStatusCodeIsItsOwnSetting(t *testing.T) {
	// A user who wants failures apart from no-match by status alone sets the error status; no-match keeps its own.
	m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Reply(OK().BodyFile("missing.txt")))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusInternalServerError, status)

	status, _ = get(t, m, "/nothing")
	require.Equal(t, StatusNoMatch, status)
}

func TestHandler_FailuresSkipTheStepsOfTheMock(t *testing.T) {
	// The steps of a mock change its answer. A failure is vlatte's answer, so the steps must not change it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Step(appendStep("<step>")).Reply(OK().BodyFile("missing.txt")))
	m.MustMock(Getf("/mapper").Step(appendStep("<step>")).Reply(OK()).
		Map(func(*RequestValues, *Response) error { return errors.New("mapper failed") }))

	for _, path := range []string{"/x", "/mapper"} {
		status, header, body := getFull(t, m, path)
		require.Equal(t, StatusNoMatch, status, body)
		require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
		require.NotContains(t, body, "<step>")
	}
}

// failingStore is a store whose List fails.
type failingStore struct {
	*vlatte.MemoryStore[*Mock]
}

func (failingStore) List(context.Context) (iter.Seq[*Mock], error) {
	return nil, errors.New("store down")
}

func (failingStore) PickAll(context.Context) ([]*Mock, error) {
	return nil, errors.New("store down")
}

func TestHandler_StoreAndMatcherErrorsAreFailures(t *testing.T) {
	// A store that failed answered an empty 200, and a matcher that failed answered as if no mock matched.
	// Both are failures to find a mock, so a test can't mistake them for a mock's answer or for a missing mock.
	t.Run("store", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}
		m.MustStart()
		t.Cleanup(m.Close)

		status, header, body := getFull(t, m, "/x")
		require.Equal(t, StatusNoMatch, status)
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderVlatteError))
		require.True(t, strings.HasPrefix(body, "MATCHING FAILED\n"), body)
		require.Contains(t, body, "store down")
	})

	t.Run("matcher", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		scoped := m.MustMock(Getf("/x").ID("broken").Header("test", match.Function(
			func(any) (bool, error) { return false, errors.New("matcher failed") })).Reply(OK()))

		status, header, body := getFull(t, m, "/x")
		require.Equal(t, StatusNoMatch, status)
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderVlatteError))
		require.Contains(t, body, "Mock [broken]", "the error names the mock whose matcher failed")
		require.Contains(t, body, "matcher failed")
		require.False(t, scoped.Called())
	})
}

func TestHandler_APanicIsAFailure(t *testing.T) {
	// A panicking reply answers the error status with the panic failure, and the server keeps serving.
	m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/panic").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
		panic("boom")
	})))
	m.MustMock(Getf("/ok").Reply(OK()))

	status, header, body := getFull(t, m, "/panic")
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, vlatte.FailurePanic, header.Get(HeaderVlatteError))
	require.Contains(t, body, "boom")

	status, _ = get(t, m, "/ok")
	require.Equal(t, http.StatusOK, status)
}

func TestLifecycle_ErrorLogsNameTheMock(t *testing.T) {
	// In a server with many mocks, the log is how a failure is traced to its mock.
	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger))
		m.MustStart()
		t.Cleanup(m.Close)

		m.MustMock(Getf("/x").ID("file-mock").Name("missing file").Reply(OK().BodyFile("missing.txt")))

		get(t, m, "/x")
		require.Contains(t, logs.String(), `"mock":{"id":"file-mock","name":"missing file"}`)
	})

	t.Run("descriptive", func(t *testing.T) {
		// New writes the descriptive log to stdout, so the test builds it with a buffer.
		m := MustNew()
		var out bytes.Buffer
		lifecycle := newBuiltInDescriptiveMockHTTPLifecycle(m, ansi(false), &out)

		mock, err := Getf("/x").ID("a").Name("users").Reply(OK()).Build(m)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		lifecycle.OnError(&RequestValues{ReqID: "r1", RawRequest: req, URL: req.URL, Mock: mock}, errors.New("boom"))
		require.Contains(t, out.String(), "Request ID: r1\n", "the ID a client reads in the VLatte-Request-Id header")
		require.Contains(t, out.String(), "Mock: a users\n")
		require.Contains(t, out.String(), "Error: boom")
	})
}

func TestLifecycle_TheRequestLogTimesTheEvents(t *testing.T) {
	// The request log was written before the events ran, so its elapsed time left them out, though the response waits
	// for them: a webhook that took a second logged a request of a millisecond.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("slow", vlatte.EventFunc[vlatte.OnComplete](
		func(context.Context, vlatte.OnComplete) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "slow"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)

	var matched []float64
	for line := range strings.Lines(logs.String()) {
		var entry struct {
			Message string  `json:"message"`
			Elapsed float64 `json:"elapsed"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)

		if strings.HasPrefix(entry.Message, "<--- REQUEST MATCHED") {
			matched = append(matched, entry.Elapsed)
		}
	}

	require.Len(t, matched, 1)
	require.GreaterOrEqual(t, matched[0], 100.0, "the elapsed time, in milliseconds, counts the events")
}

func TestHandler_TheResponseWaitsForTheEvents(t *testing.T) {
	// A test checks what the events of a mock did, such as a webhook's call, right after the response, with no wait of
	// its own: the response waits for them, global ones too.
	var done atomic.Int32
	slow := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
		time.Sleep(50 * time.Millisecond)
		done.Add(1)

		return nil
	})

	m := MustNew(Setup().Silent().
		OnCompleteListener("slow", slow).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "slow"}}}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "slow"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, int32(2), done.Load(), "the response came once every event was done")
}

func TestHandler_CloseCancelsTheEventsRunning(t *testing.T) {
	// Close stops the events still running, such as a webhook waiting on a slow target, instead of waiting for them:
	// the requests waiting for them end, and the server closes at once.
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan error, 1)

	m := MustNew(Setup().Silent().
		OnCompleteListener("waits", vlatte.EventFunc[vlatte.OnComplete](func(ctx context.Context, _ vlatte.OnComplete) error {
			close(started)

			select {
			case <-ctx.Done():
			case <-release: // the test failed first
			}

			canceled <- ctx.Err()

			return ctx.Err()
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	// Last in, first out: the event ends before the server closes, when the test fails first.
	t.Cleanup(func() { close(release) })

	m.MustMock(Getf("/waits").OnCompleteEvent(vlatte.EventConfig{Name: "waits"}).Reply(NoContent()))

	// The client waits for the event, in a goroutine of its own. Close closes its connection, so it gets no response.
	ended := make(chan struct{})
	go func() {
		defer close(ended)

		if res, err := http.Get(m.URL("/waits")); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the event never started")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		m.Close()
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the event instead of canceling it")
	}

	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("Close returned before the event it canceled")
	}

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the request waiting for the event never ended")
	}
}

func TestHandler_EventsRunForRepliesWithoutAResponse(t *testing.T) {
	// Handler replies return no response. The events of their mock run all the same, and the request log, which read
	// the response and panicked (#24), logs the match. Silent() would skip the request log.
	var ran atomic.Bool
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("hook", vlatte.EventFunc[vlatte.OnComplete](
		func(context.Context, vlatte.OnComplete) error {
			ran.Store(true)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").
		OnCompleteEvent(vlatte.EventConfig{Name: "hook"}).
		Reply(Handler(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusOK, status)
	require.True(t, ran.Load(), "the event ran before the response ended")
	require.Contains(t, logs.String(), "REQUEST MATCHED GET /x")
	require.NotContains(t, logs.String(), "panic")
}

func TestLifecycle_RepliesWithoutAResponseAreLogged(t *testing.T) {
	// #24: Echo, Handler and a Func that returns no response write their own response. Both request logs read that
	// response and panicked on every such request: the recovery middleware tried to answer 418 after the reply had
	// answered, and the log held a stack trace instead of the match. The log tells the status the client got.
	replies := []struct {
		name  string
		reply ReplierFactory
		want  int
	}{
		{
			name:  "echo",
			reply: Echo(),
			want:  http.StatusOK,
		},
		{
			name:  "handler",
			reply: Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }),
			want:  http.StatusCreated,
		},
		{
			name: "func without a response",
			reply: Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
				w.WriteHeader(http.StatusAccepted)
				return nil, nil
			}),
			want: http.StatusAccepted,
		},
	}

	for _, rc := range replies {
		t.Run("structured/"+rc.name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/x").ID("x").Reply(rc.reply))

			status, _ := get(t, m, "/x")
			require.Equal(t, rc.want, status)
			require.Contains(t, logs.String(), "REQUEST MATCHED GET /x")
			require.Contains(t, logs.String(), `"mock":{"id":"x"}`)
			require.Contains(t, logs.String(), `"response":{"status":`+strconv.Itoa(rc.want)+`}`)
			require.NotContains(t, logs.String(), "panic")
		})
	}

	for _, rc := range replies {
		t.Run("descriptive/"+rc.name, func(t *testing.T) {
			// The descriptive log, and the recovery middleware's, go to os.Stdout, which the server takes when it's made.
			r, w, err := os.Pipe()
			require.NoError(t, err)

			stdout := os.Stdout
			os.Stdout = w
			m := MustNew(Setup().UseDescriptiveLogger().Colors(false).LogVerbosity(vlatte.LogBody))
			os.Stdout = stdout

			m.MustStart()
			m.MustMock(Getf("/x").ID("x").Reply(rc.reply))

			status, _ := get(t, m, "/x")
			require.Equal(t, rc.want, status)

			m.Close()
			require.NoError(t, w.Close())

			out, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Contains(t, string(out), "REQUEST MATCHED")
			require.Contains(t, string(out), "Mock: x")
			require.Contains(t, string(out), "Status: "+strconv.Itoa(rc.want)+"\n")
			require.NotContains(t, string(out), "panic")
		})
	}
}

func TestLifecycle_LogsWhatTheVerbosityAsksFor(t *testing.T) {
	// Both request logs print the parts of the response the verbosity names: the headers and trailers, then the body.
	// The descriptive log doesn't print an encoded body.
	mocks := func(m *Server) {
		m.MustMock(
			Getf("/plain").Reply(OK().Header("X-Head", "h").Trailer("X-Tail", "t").PlainText("plain body")),
			Getf("/zipped").Reply(OK().PlainText("zipped body").Gzip()),
		)
	}

	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
		m.MustStart()
		t.Cleanup(m.Close)
		mocks(m)

		get(t, m, "/plain")
		get(t, m, "/zipped")

		out := logs.String()
		require.Contains(t, out, `"X-Head":["h"]`)
		require.Contains(t, out, `"trailer":{"X-Tail":["t"]}`)
		require.Contains(t, out, `"body":"plain body"`)
		require.Contains(t, out, `"encoding":"gzip"`)
	})

	t.Run("descriptive", func(t *testing.T) {
		// The descriptive log goes to os.Stdout, which the server takes when it's made.
		r, w, err := os.Pipe()
		require.NoError(t, err)

		stdout := os.Stdout
		os.Stdout = w
		m := MustNew(Setup().UseDescriptiveLogger().Colors(false).LogVerbosity(vlatte.LogBody))
		os.Stdout = stdout

		m.MustStart()
		mocks(m)

		get(t, m, "/plain")
		get(t, m, "/zipped")

		m.Close()
		require.NoError(t, w.Close())

		out, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Contains(t, string(out), "Headers: ")
		require.Contains(t, string(out), "X-Head")
		require.Contains(t, string(out), "Trailers: ")
		require.Contains(t, string(out), "X-Tail")
		require.Contains(t, string(out), "Body: plain body")
		require.Contains(t, string(out), "Body: <encoded body omitted>")
	})
}

// cancelableKV is a parameter store that fails once its context is done, as a remote store does.
type cancelableKV struct {
	vlatte.KV
}

func (kv cancelableKV) Get(ctx context.Context, k string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return kv.KV.Get(ctx, k)
}

func TestHandler_EventsOutliveTheClient(t *testing.T) {
	// A client that leaves, as one that times out does, ends the context of its request, not the events of the
	// request: they run with a context of their own, and their templates read the parameters with it, through a store
	// that fails once its context is done.
	kv := cancelableKV{vlatte.NewMemoryKV()}
	require.NoError(t, kv.Set(t.Context(), "target", []byte("api")))

	started := make(chan struct{})
	rendered := make(chan string, 1)

	m := MustNew(Setup().Silent().Parameters(kv).OnCompleteListener("renders", vlatte.EventFunc[vlatte.OnComplete](
		func(ctx context.Context, e vlatte.OnComplete) error {
			close(started)

			select {
			case <-e.(*OnComplete).Request().RawRequest.Context().Done():
			case <-ctx.Done(): // Close, when the test fails first
				return ctx.Err()
			}

			var b strings.Builder

			tmpl, err := e.Kit().TemplateEngine().Compile("target", `{{ .Parameters.Get "target" }}`)
			if err == nil {
				err = tmpl.Execute(&b, e.Kit().Data())
			}

			if err == nil {
				err = ctx.Err()
			}

			if err != nil {
				b.WriteString(err.Error())
			}

			rendered <- b.String()

			return err
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "renders"}).Reply(NoContent()))

	ctx, leave := context.WithCancel(t.Context())
	defer leave()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/x"), nil)
	require.NoError(t, err)

	ended := make(chan struct{})
	go func() {
		defer close(ended)

		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the event never started")
	}

	leave()
	<-ended

	select {
	case got := <-rendered:
		require.Equal(t, "api", got)
	case <-time.After(5 * time.Second):
		t.Fatal("the event never saw the client leave")
	}
}

type tenantKey struct{}

// tenantKV is a parameter store scoped to the tenant of each request: a read without one fails.
type tenantKV struct {
	vlatte.KV
}

func (kv tenantKV) Get(ctx context.Context, k string) ([]byte, error) {
	if ctx.Value(tenantKey{}) == nil {
		return nil, errors.New("no tenant")
	}

	return kv.KV.Get(ctx, k)
}

func TestHandler_EventsSeeTheValuesOfTheRequestContext(t *testing.T) {
	// Middlewares put request-scoped values in the request's context, such as a trace or a tenant, which listeners and
	// parameter stores read: the events of the request see them.
	kv := tenantKV{vlatte.NewMemoryKV()}
	require.NoError(t, kv.Set(t.Context(), "target", []byte("api")))

	type seen struct {
		tenant   any
		rendered string
	}

	got := make(chan seen, 1)

	m := MustNew(Setup().Silent().Parameters(kv).
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, "acme")))
			})
		}).
		OnCompleteListener("reads", vlatte.EventFunc[vlatte.OnComplete](func(ctx context.Context, e vlatte.OnComplete) error {
			var b strings.Builder

			tmpl, err := e.Kit().TemplateEngine().Compile("target", `{{ .Parameters.Get "target" }}`)
			if err == nil {
				err = tmpl.Execute(&b, e.Kit().Data())
			}

			if err != nil {
				b.WriteString(err.Error())
			}

			got <- seen{tenant: ctx.Value(tenantKey{}), rendered: b.String()}

			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "reads"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)

	s := <-got
	require.Equal(t, "acme", s.tenant)
	require.Equal(t, "api", s.rendered)
}

func TestHandler_DebugHeadersNameTheMockThatMatched(t *testing.T) {
	// A client sees which mock answered it, how often that mock did, and which file to edit, without the server's logs.
	path := filepath.Join(t.TempDir(), "users.mock.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"id": "users", "name": "list users", "request": {"path": "/users"}}`), 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromFile(path), Getf("/plain").ID("plain").Reply(OK()), Getf("/fails").ID("fails").Reply(OK().BodyFile("missing.txt")))

	_, header, _ := getFull(t, m, "/users")
	require.Equal(t, "users", header.Get(vlatte.HeaderMatchedMockID))
	require.Equal(t, "list users", header.Get(vlatte.HeaderMatchedMockName))
	require.Equal(t, "1", header.Get(vlatte.HeaderMatchedMockHits), "the hits count this request")
	require.Equal(t, path, header.Get(vlatte.HeaderMatchedMockSource))

	_, header, _ = getFull(t, m, "/users")
	require.Equal(t, "2", header.Get(vlatte.HeaderMatchedMockHits))

	_, header, _ = getFull(t, m, "/plain")
	require.Equal(t, "plain", header.Get(vlatte.HeaderMatchedMockID))
	require.Equal(t, "1", header.Get(vlatte.HeaderMatchedMockHits), "each mock counts its own")
	require.Empty(t, header.Values(vlatte.HeaderMatchedMockName), "a mock with no name sends none")
	require.Empty(t, header.Values(vlatte.HeaderMatchedMockSource), "nor a mock built in code a source")

	_, header, _ = getFull(t, m, "/fails")
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.Equal(t, "fails", header.Get(vlatte.HeaderMatchedMockID), "a reply that fails names its mock too")
}

func TestHandler_RequestIDHeaderFindsTheRequestInTheLogs(t *testing.T) {
	// The header carries the ID that templates and logs see, on every answer, failures and no-match included, so a
	// client finds its request in the logs.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/id").Reply(OK().BodyTemplate(`{{ .Request.ID }}`)), Getf("/fails").Reply(OK().BodyFile("missing.txt")))

	_, header, body := getFull(t, m, "/id")
	id := header.Get(vlatte.HeaderRequestID)
	require.NotEmpty(t, id)
	require.Equal(t, body, id, "templates see the same ID")
	require.Contains(t, logs.String(), `"request_id":"`+id+`"`)

	seen := map[string]bool{id: true}
	for _, path := range []string{"/fails", "/nothing"} {
		_, header, _ := getFull(t, m, path)
		id := header.Get(vlatte.HeaderRequestID)
		require.NotEmpty(t, id, path)
		require.False(t, seen[id], "%s: each request has an ID of its own", path)
		require.Contains(t, logs.String(), `"request_id":"`+id+`"`, path)

		seen[id] = true
	}
}

func TestHandler_ProxiedHeaderMarksTheAnswersOfARealService(t *testing.T) {
	// A proxied answer looks like a mock's: only the header tells the client that a real service sent it.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "real") })

	t.Run("a mock that forwards", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		m.MustMock(Getf("/forward").ID("forward").Reply(From(target.URL)), Getf("/mocked").Reply(OK()))

		_, header, body := getFull(t, m, "/forward")
		require.Equal(t, "real", body)
		require.Equal(t, "true", header.Get(vlatte.HeaderProxied))
		require.Equal(t, "forward", header.Get(vlatte.HeaderMatchedMockID), "it names its mock")

		_, header, _ = getFull(t, m, "/mocked")
		require.Empty(t, header.Values(vlatte.HeaderProxied), "a mock's own answer isn't proxied")
	})

	t.Run("a request no mock matches, on a proxy", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy())
		m.MustStart()
		t.Cleanup(m.Close)

		proxyURL, err := url.Parse(m.URL())
		require.NoError(t, err)

		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		t.Cleanup(transport.CloseIdleConnections)

		res, err := (&http.Client{Transport: transport}).Get(target.URL + "/other")
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, "true", res.Header.Get(vlatte.HeaderProxied))
		require.Empty(t, res.Header.Values(vlatte.HeaderMatchedMockID), "no mock matched")
	})

	t.Run("a recording's target", func(t *testing.T) {
		m := mockedServer(t, Setup().Silent())
		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

		res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
		require.Equal(t, "real", string(body))
		require.Equal(t, "true", res.Header.Get(vlatte.HeaderProxied))

		stopRecording(t, m)
	})
}

func TestHandler_NoDebugHeadersTurnsThemAllOff(t *testing.T) {
	// The switch is all or nothing, such as for a client that compares whole answers. A failure still tells itself
	// apart from a mock's answer: that header isn't a debug one.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "real") })

	m := MustNew(Setup().Silent().NoDebugHeaders())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/x").ID("x").Name("x").SetSource("mocks/x.yaml").Reply(OK()),
		Getf("/forward").Reply(From(target.URL)),
	)

	for _, path := range []string{"/x", "/forward", "/nothing"} {
		_, header, _ := getFull(t, m, path)
		for _, name := range _debugHeaders {
			require.Empty(t, header.Values(name), "%s: %s", path, name)
		}
	}

	_, header, _ := getFull(t, m, "/nothing")
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
}

func TestHandler_StepsDropTheContentLengthOfAReplyThatWritesItsOwn(t *testing.T) {
	// A step can change how many bytes go out, so the Content-Length a reply declares would be wrong: the server
	// failed the write. Steps drop it, whatever reply set it, a proxy's or one that writes its own response.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/h").Step(appendStep("<step>")).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "5")
		_, _ = io.WriteString(w, "hello")
	})))

	status, body := get(t, m, "/h")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "hello<step>", body)
}

func TestHandler_AFlushSendsTheAnswerSoFar(t *testing.T) {
	// A Handler reply that streams flushes with w.(http.Flusher), as handlers written before http.ResponseController
	// do: the client gets the status and the headers at once, before the reply ends.
	received := make(chan struct{})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/stream").Reply(Handler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Stream", "on")
		w.(http.Flusher).Flush()

		// The rest waits for the client to have the answer, which only the flush sent.
		select {
		case <-received:
		case <-r.Context().Done():
			return
		}

		_, _ = io.WriteString(w, "rest")
	})))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/stream"), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "the answer waited for the reply to end")
	close(received)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "on", res.Header.Get("X-Stream"))
	require.Equal(t, "rest", string(body))
}

func TestHandler_ABodyCopiedPastAWriterThatCantReadFromArrivesWhole(t *testing.T) {
	// A middleware's writer seldom reads from a reader, as net/http's does: a body the reply copies, such as a body
	// file's, still reaches the client whole, and the journal has the status the client got.
	body := bytes.Repeat([]byte("0123456789abcdef"), 8<<10)
	file := filepath.Join(t.TempDir(), "body.bin")
	require.NoError(t, os.WriteFile(file, body, 0o600))

	m := journaledServer(t, Setup().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(opaqueWriter{w}, r) })
	}))
	m.MustMock(Getf("/file").Reply(Created().BodyFile(file)))

	res, got := do(t, http.MethodGet, m.URL("/file"), nil, nil)
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, body, got)

	e := only(t, m, journal.Query{Path: match.Equal("/file")})
	require.Equal(t, journal.OutcomeMatched, e.Outcome)
	require.Equal(t, http.StatusCreated, e.Status)
}

func TestHandler_APushGoesToTheWriterThatCanPush(t *testing.T) {
	// A Handler reply pushes with w.(http.Pusher), as on a server of its own. Over HTTP/2, the push reaches HTTP/2's
	// writer, which checks it: here it refuses the promise of a POST. HTTP/1.1 can't push: Push says so with
	// http.ErrNotSupported, as net/http's own writer would, and the reply goes on to answer.
	pushed := make(chan error, 1)

	m := MustNew(Setup().Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/page").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		pushed <- w.(http.Pusher).Push("/style.css", &http.PushOptions{Method: http.MethodPost})
		w.WriteHeader(http.StatusNoContent)
	})))

	// push requests the page over HTTP/protoMajor, and returns what Push returned to the reply.
	push := func(t *testing.T, protoMajor int) error {
		t.Helper()

		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, Protocols: new(http.Protocols)}
		tr.Protocols.SetHTTP1(protoMajor == 1)
		tr.Protocols.SetHTTP2(protoMajor == 2)
		t.Cleanup(tr.CloseIdleConnections)

		res, err := (&http.Client{Transport: tr}).Get(m.URL("/page"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		require.Equal(t, protoMajor, res.ProtoMajor)
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		return <-pushed
	}

	t.Run("HTTP/1.1, which can't push", func(t *testing.T) {
		require.ErrorIs(t, push(t, 1), http.ErrNotSupported)
	})

	t.Run("HTTP/2, which checks the push", func(t *testing.T) {
		require.EqualError(t, push(t, 2), `method "POST" must be GET or HEAD`)
	})
}

func TestHandler_TheResponseWaitsForTheCallbacks(t *testing.T) {
	// A callback is an on_complete event of its mock: the response waits for it, so a test checks what it did right
	// after the call, with no wait of its own.
	entered := make(chan struct{})
	release := make(chan struct{})

	var done atomic.Bool

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").
		Callback(func(context.Context, vlatte.OnComplete) error {
			close(entered)
			<-release
			done.Store(true)

			return nil
		}).
		Reply(OK().PlainText("ok")))

	// The client runs in a goroutine of its own: it sends what it got, and the test asserts it.
	responded := make(chan int, 1)
	go func() {
		res, err := http.Get(m.URL("/x"))
		if err != nil {
			responded <- 0
			return
		}

		_ = res.Body.Close()
		responded <- res.StatusCode
	}()

	select {
	case <-entered:
	case status := <-responded:
		t.Fatalf("the response, %d, came before the callback ran", status)
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never ran")
	}

	select {
	case status := <-responded:
		t.Fatalf("the response, %d, came while the callback ran", status)
	default:
	}

	close(release)
	require.Equal(t, http.StatusOK, <-responded)
	require.True(t, done.Load(), "the response came once the callback was done")
}

func TestHandler_APanickingCallbackIsAWarning(t *testing.T) {
	// A callback's panic is logged, as an event's is: the client gets the mock's answer, the other callbacks and
	// events of the mock run, and the request is logged as matched.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	var ran atomic.Int32

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("counts", vlatte.EventFunc[vlatte.OnComplete](
		func(context.Context, vlatte.OnComplete) error {
			ran.Add(1)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/panics").
		Callback(func(context.Context, vlatte.OnComplete) error { panic("boom") }).
		Callback(func(context.Context, vlatte.OnComplete) error {
			ran.Add(1)
			return nil
		}).
		OnCompleteEvent(vlatte.EventConfig{Name: "counts"}).
		Reply(OK().PlainText("ok")))

	status, body := get(t, m, "/panics")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", body)
	require.Equal(t, int32(2), ran.Load(), "the other callback and the event ran")
	require.Contains(t, logs.String(), "on_complete listener callback at index 0: panic: boom")
	require.Contains(t, logs.String(), "REQUEST MATCHED GET /panics")

	status, _ = get(t, m, "/panics")
	require.Equal(t, http.StatusOK, status, "the server keeps serving")
}

func TestHandler_ACallbackThatExitsItsGoroutineStillGetsTheResponse(t *testing.T) {
	// A failing require in a callback calls t.FailNow, which exits the goroutine it runs on (#35). It ran on the
	// handler's, and the client got no response. It exits the callback's own goroutine now: the client gets its answer.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/exits").
		Callback(func(context.Context, vlatte.OnComplete) error {
			runtime.Goexit()
			return nil
		}).
		Reply(OK().PlainText("ok")))

	status, body := get(t, m, "/exits")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", body)
}

func TestHandler_CallbacksOutliveTheClient(t *testing.T) {
	// A client that leaves, as one that times out does, ends the context of its request, not the callbacks of the
	// request: they run with the context events get.
	started := make(chan struct{})
	release := make(chan struct{})
	seen := make(chan error, 1)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	// Last in, first out: the callback ends before the server closes, when the test fails first.
	t.Cleanup(func() { close(release) })

	m.MustMock(Getf("/x").
		Callback(func(ctx context.Context, in vlatte.OnComplete) error {
			close(started)

			select {
			case <-in.(*OnComplete).Request().RawRequest.Context().Done():
			case <-release:
			}

			seen <- ctx.Err()

			return nil
		}).
		Reply(NoContent()))

	ctx, leave := context.WithCancel(t.Context())
	defer leave()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/x"), nil)
	require.NoError(t, err)

	ended := make(chan struct{})
	go func() {
		defer close(ended)

		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never started")
	}

	leave()
	<-ended

	select {
	case err := <-seen:
		require.NoError(t, err, "the client leaving ended the callback's context")
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never saw the client leave")
	}
}

func TestHandler_CloseCancelsTheCallbacksRunning(t *testing.T) {
	// Close stops the callbacks still running, as it stops events, instead of waiting for them.
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan error, 1)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	t.Cleanup(func() { close(release) })

	m.MustMock(Getf("/waits").
		Callback(func(ctx context.Context, _ vlatte.OnComplete) error {
			close(started)

			select {
			case <-ctx.Done():
			case <-release: // the test failed first
			}

			canceled <- context.Cause(ctx)

			return ctx.Err()
		}).
		Reply(NoContent()))

	go func() {
		if res, err := http.Get(m.URL("/waits")); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never started")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		m.Close()
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the callback instead of canceling it")
	}

	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("Close returned before the callback it canceled")
	}
}

func TestHandler_CallbacksRunAlongsideTheEvents(t *testing.T) {
	// Callbacks are events of their mock: they run at the same time as each other, the mock's named events and the
	// global ones, so none holds up another. Each waits here until all four run: one after the other, none would see
	// the others. A failing one is logged under its index, and the others run all the same.
	const runners = 4

	var arrived atomic.Int32

	all := make(chan struct{})
	together := make(chan bool, runners)

	meet := func() error {
		if arrived.Add(1) == runners {
			close(all)
		}

		select {
		case <-all:
			together <- true
		case <-time.After(5 * time.Second):
			together <- false
		}

		return nil
	}

	logs := &logBuffer{}
	logger := zerolog.New(logs)

	meets := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return meet() })
	m := MustNew(Setup().Logger(&logger).
		OnCompleteListener("meets", meets).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "meets"}}}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").
		Callback(func(context.Context, vlatte.OnComplete) error { return meet() }).
		Callback(func(context.Context, vlatte.OnComplete) error {
			_ = meet()
			return errors.New("failed")
		}).
		OnCompleteEvent(vlatte.EventConfig{Name: "meets"}).
		Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)

	for range runners {
		require.True(t, <-together, "a callback or an event waited for another")
	}

	require.Contains(t, logs.String(), "on_complete listener callback at index 1: failed")
}

func TestHandler_CallbacksSeeTheServedRequest(t *testing.T) {
	// A callback gets what an event gets: the mock, its hits counting the request, and the response as it was sent,
	// after the mappers. A reply that writes its own response, such as Handler, returns none. A callback has no
	// parameters: a mock file can't set it.
	type view struct {
		kind     string
		hits     int64
		status   int
		body     string
		params   vlatte.Parameters
		response bool
	}

	views := make(chan view, 2)
	callback := func(_ context.Context, e vlatte.OnComplete) error {
		in, ok := e.(*OnComplete)
		if !ok {
			views <- view{kind: fmt.Sprintf("%T", e)}
			return nil
		}

		v := view{kind: in.Kind().String(), hits: in.Mock().Hits(), params: in.Parameters(), response: in.Response() != nil}

		if res := in.Response(); res != nil {
			v.status, v.body = res.StatusCode, string(res.Body)
		}

		views <- v

		return nil
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/mapped").
			Map(func(_ *RequestValues, res *Response) error {
				res.StatusCode = http.StatusAccepted
				res.Body = []byte("mapped")

				return nil
			}).
			Callback(callback).
			Reply(OK().PlainText("ok")),
		Getf("/handler").
			Callback(callback).
			Reply(Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })),
	)

	status, body := get(t, m, "/mapped")
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, "mapped", body)
	require.Equal(t, view{kind: "http", hits: 1, status: http.StatusAccepted, body: "mapped", response: true}, <-views)

	status, _ = get(t, m, "/handler")
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, view{kind: "http", hits: 1}, <-views)
}

func TestHandler_CallbacksRunOnlyForAServedMock(t *testing.T) {
	// A callback runs once its mock served the request. A request it doesn't match, or that its mock fails to serve,
	// runs none. The response waits for callbacks, so one that ran would have run by then.
	tests := []struct {
		name    string
		give    *Builder
		target  string
		want    int
		wantRan int32
	}{
		{
			name:   "no match",
			give:   Getf("/x").Reply(OK()),
			target: "/y",
			want:   StatusNoMatch,
		},
		{
			name: "failing reply",
			give: Getf("/x").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
				return nil, errors.New("failed")
			})),
			target: "/x",
			want:   http.StatusInternalServerError,
		},
		{
			name: "failing mapper",
			give: Getf("/x").
				Map(func(*RequestValues, *Response) error { return errors.New("failed") }).
				Reply(OK()),
			target: "/x",
			want:   http.StatusInternalServerError,
		},
		{
			name:    "served",
			give:    Getf("/x").Reply(OK()),
			target:  "/x",
			want:    http.StatusOK,
			wantRan: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran atomic.Int32

			m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(tt.give.Callback(func(context.Context, vlatte.OnComplete) error {
				ran.Add(1)
				return nil
			}))

			status, _ := get(t, m, tt.target)
			require.Equal(t, tt.want, status)
			require.Equal(t, tt.wantRan, ran.Load())
		})
	}
}

func TestHandler_AScenarioMovesBeforeItsMockAnswers(t *testing.T) {
	// A client that gets part of the answer, as of a large or streamed body, and calls again at once must match in the
	// new state: a request sent while the mock still answers already does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	during := make(chan int, 1)
	m.MustMock(
		Getf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").
			Reply(Handler(func(w http.ResponseWriter, r *http.Request) {
				res, err := _rawClient.Get(m.URL("/receipt"))
				if err != nil {
					during <- 0
					return
				}

				res.Body.Close()
				during <- res.StatusCode
				w.WriteHeader(http.StatusOK)
			})),
		Getf("/receipt").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(Status(http.StatusAccepted)),
	)

	res, _ := do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, http.StatusAccepted, <-during, "the receipt of paid answered while pay still answered")
}

func TestNoMatch_ListsTheClosestMocks(t *testing.T) {
	// A request no mock matched is the commonest puzzle of a test: the answer names the mocks that came closest,
	// and the parts of the request that differ, so the fix is read off the answer.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/users").ID("users").Name("list users").Header("Accept", match.Equal("application/json")).Reply(OK()),
		Postf("/users").ID("create").Reply(Created()),
		Getf("/orders").ID("orders").Query("page", match.Equal("2")).Reply(OK()),
	)

	status, header, body := getFull(t, m, "/users")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
	require.Equal(t, "REQUEST DID NOT MATCH\nGET "+m.URL("/users")+"\n\n"+
		"CLOSEST MOCKS\n"+
		"[users] list users: 2 of 3 parts matched; differs in header Accept\n"+
		"[create]: 1 of 2 parts matched; differs in method\n"+
		"[orders]: 1 of 3 parts matched; differs in path, query page\n", body)
}

func TestNoMatch_NearMissesRunNoMatcherTwice(t *testing.T) {
	// The report goes on from where matching stopped: a matcher with an action, such as Peek's, runs once for a
	// request no mock matched, as it does for one a mock matched.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	var runs int
	peek := match.Peek(match.Equal("1"), func(any) error { runs++; return nil })

	m.MustMock(
		Getf("/a").ID("a").Header("x", peek).Reply(OK()),
		Postf("/b").ID("b").Header("x", peek).Reply(OK()),
	)

	status, _, body := getFull(t, m, "/a")
	require.Equal(t, StatusNoMatch, status)
	require.Contains(t, body, "[a]: 2 of 3 parts matched; differs in header x\n")
	require.Equal(t, 2, runs, "the header of each mock ran once: a's in matching, b's in the report")
}

func TestNoMatch_NearMissesSayWhereTheScenarioIs(t *testing.T) {
	// A request that waits on a scenario's state reads why no mock answered: the state the mock needs, and the one the
	// scenario is in. The report runs the scenario's matcher, which moves nothing: the next request still finds the
	// scenario where it was.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("a").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(OK()),
		Getf("/b").ID("b").StartScenario("checkout").ScenarioStateWillBe("paid").Reply(Status(http.StatusAccepted)),
	)

	status, _, body := getFull(t, m, "/a")
	require.Equal(t, StatusNoMatch, status)
	require.Contains(t, body, "[a]: 2 of 3 parts matched; differs in scenario checkout needs paid, is started\n")

	status, _, _ = getFull(t, m, "/b")
	require.Equal(t, http.StatusAccepted, status, "the scenario is still where it started")
}

func TestNoMatch_NearMissesTurnOff(t *testing.T) {
	// The report runs the matchers matching never reached: a server with many mocks, or matchers with actions, turns
	// it off, in code or in its settings.
	for name, setup := range map[string]*ConfigBuilder{
		"builder": Setup().Silent().NoNearMisses(),
		"setting": Setup().Silent().ConfigSources(ConfigArgs([]string{"--near-misses=false"})),
	} {
		t.Run(name, func(t *testing.T) {
			m := MustNew(setup)
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/a").Reply(OK()))

			status, _, body := getFull(t, m, "/b")
			require.Equal(t, StatusNoMatch, status)
			require.Equal(t, "REQUEST DID NOT MATCH\nGET "+m.URL("/b")+"\n\n", body)
		})
	}
}

func TestNoMatch_NamesEachPartAsTheBuilderDoes(t *testing.T) {
	// A part is named as a test wrote it, Times and RequestMatches included, and a matcher's error stays on its
	// mock's line, whatever it holds. A Once used up is the part a mock differs in: matching ran it. Matching stops
	// at the path, before the predicate: only the report runs it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("once").Once().Reply(OK()),
		Getf("/c").ID("predicate").RequestMatches(func(*http.Request) (bool, error) {
			return false, errors.New("bad\nrequest")
		}).Reply(OK()),
	)

	status, _ := get(t, m, "/a")
	require.Equal(t, http.StatusOK, status)

	_, _, body := getFull(t, m, "/a")
	require.Contains(t, body, "\n[once]: 2 of 3 parts matched; differs in times\n")

	_, _, body = getFull(t, m, "/b")
	require.Contains(t, body, "\n[predicate]: 1 of 3 parts matched; differs in path, request: ")
	require.Contains(t, body, "bad request\n")
}

func TestNoMatch_NamesAKeyedPartWithItsKey(t *testing.T) {
	// A part of many keys names the one a mock differs in, as a header does: "differs in cookie" would leave a test
	// to find which cookie.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("headers").Headers("X-Id", match.Equal([]string{"1"})).Reply(OK()),
		Getf("/a").ID("cookie").Cookie("session", match.Equal("s")).Reply(OK()),
		Request().ID("param").Method(http.MethodGet).URLPathTemplate("/{id}").PathParam("id", match.Equal("b")).Reply(OK()),
	)

	_, _, body := getFull(t, m, "/a")
	require.Contains(t, body, "\n[headers]: 2 of 3 parts matched; differs in headers X-Id\n")
	require.Contains(t, body, "\n[cookie]: 2 of 3 parts matched; differs in cookie session\n")
	require.Contains(t, body, "\n[param]: 2 of 3 parts matched; differs in path_params id\n")
}

// failingStateMatcher is a matcher that keeps state, matches every request, and fails to move its state on.
type failingStateMatcher struct{}

func (failingStateMatcher) Name() string                          { return "failing state" }
func (failingStateMatcher) Match(vlatte.MatcherArg) (bool, error) { return true, nil }
func (failingStateMatcher) SetState() error                       { return errors.New("state store down") }

func TestHandler_AMatcherFailingToMoveItsStateOnIsAWarning(t *testing.T) {
	// A matcher that can't move its state on once the mock served, as a scenario whose store fails, doesn't fail an
	// answer already chosen: the client gets it, the mock counts, and the log says which matcher failed and why. A
	// stream, counted as it starts, warns the same.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()

	plain := m.MustMock(Getf("/plain").Matches(failingStateMatcher{}).Reply(OK().BodyText("ok")))
	stream := m.MustMock(Getf("/stream").Matches(failingStateMatcher{}).Reply(OK().EventStream(NewEventStream(NewEvent().Data("a")))))

	_, body := fetch(t, m.URL("/plain"))
	require.Equal(t, "ok", string(body))

	_, body = fetch(t, m.URL("/stream"))
	require.Equal(t, "data: a\n\n", string(body))

	// Close waits for the handlers, and so for their logs.
	m.Close()

	require.Equal(t, int64(1), plain.Hits())
	require.Equal(t, int64(1), stream.Hits())
	require.Equal(t, 2, strings.Count(logs.String(), "http: after mock served event: matcher[2] state store down"), logs.String())
}
