package mockhttp

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

// _misses recycles the misses of the requests no mock matches, which matching keeps for every request.
var _misses = sync.Pool{New: func() any { return new(vlatte.Misses[*Mock, *selectorInput]) }}

var (
	_ http.ResponseWriter = (*topResponseWriter)(nil)
	_ http.Flusher        = (*topResponseWriter)(nil)
	_ http.Hijacker       = (*topResponseWriter)(nil)
	_ vlatte.Stage        = responseSink{}
	_ http.Flusher        = (*answerWriter)(nil)
	_ http.Hijacker       = (*answerWriter)(nil)
	_ http.Pusher         = (*answerWriter)(nil)
	_ io.ReaderFrom       = (*answerWriter)(nil)
)

// errAbandoned ends the flow of the steps of a mock whose reply failed or panicked: a failure is vlatte's answer, not
// the mock's.
var errAbandoned = errors.New("http: the reply ended before its steps did")

// topResponseWriter passes the body a reply writes, and its flushes, through one flow of the steps of its mock, then
// to rw. A stage may write from a goroutine of its own, so mu guards rw: every call to it, the wrapper's and the
// flow's sink's, takes mu. The reply sees an http.ResponseWriter as any other, and needs no lock of its own.
type topResponseWriter struct {
	mu         sync.Mutex
	rw         http.ResponseWriter
	flow       *vlatte.Flow
	headerSent bool
	ended      bool
}

// newTopResponseWriter opens the flow of steps, within ctx, in front of rw.
func newTopResponseWriter(ctx context.Context, steps []vlatte.Step, rw http.ResponseWriter) *topResponseWriter {
	w := &topResponseWriter{rw: rw}
	w.flow = vlatte.NewFlow(ctx, responseSink{w}, steps)

	return w
}

func (w *topResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.rw.Header()
}

// WriteHeader sends the status and the headers. Steps can change how many bytes go out, so a Content-Length, such as
// a proxy's target sends, would be wrong: the server measures the body instead, or sends it chunked.
func (w *topResponseWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.rw.Header().Del(HeaderContentLength)
	w.rw.WriteHeader(statusCode)

	if statusCode >= http.StatusOK || statusCode == http.StatusSwitchingProtocols {
		w.headerSent = true
	}
}

// Write passes p through the steps, then to rw, after the headers.
func (w *topResponseWriter) Write(p []byte) (int, error) {
	w.sendHeader()
	return w.flow.Write(p)
}

// FlushError passes the flush through the steps, then flushes rw, after the headers: a stage that delays the body
// delays its flushes too.
func (w *topResponseWriter) FlushError() error {
	w.sendHeader()
	return w.flow.Flush()
}

// Flush implements http.Flusher, as FlushError does.
func (w *topResponseWriter) Flush() {
	_ = w.FlushError()
}

// Hijack takes the connection over from rw, under the lock.
func (w *topResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return http.NewResponseController(w.rw).Hijack()
}

// SetReadDeadline sets the read deadline of rw, under the lock.
func (w *topResponseWriter) SetReadDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return http.NewResponseController(w.rw).SetReadDeadline(deadline)
}

// SetWriteDeadline sets the write deadline of rw, under the lock.
func (w *topResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return http.NewResponseController(w.rw).SetWriteDeadline(deadline)
}

// EnableFullDuplex has rw read the request while it writes the response, under the lock.
func (w *topResponseWriter) EnableFullDuplex() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return http.NewResponseController(w.rw).EnableFullDuplex()
}

// Unwrap returns the response writer, so captureWriterOf finds the capture under it. http.ResponseController finds
// the wrapper's own methods first.
func (w *topResponseWriter) Unwrap() http.ResponseWriter { return w.rw }

// Context returns the flow's context: it ends when the request's does, or a stage aborts the flow.
func (w *topResponseWriter) Context() context.Context { return w.flow.Context() }

// sendHeader sends the status, 200 unless the reply sent one, before the first bytes or flush enter the flow. The
// server then writes from its own copy of the headers, so the reply setting one later, such as a trailer, doesn't
// race a stage that writes from a goroutine of its own.
func (w *topResponseWriter) sendHeader() {
	if !w.headerSent {
		w.WriteHeader(http.StatusOK)
	}
}

// end ends the flow once the reply wrote all it will: its stages pass on what they hold, and end. It returns once the
// last did, so nothing writes to rw after it.
func (w *topResponseWriter) end() error {
	w.ended = true
	return w.flow.Close()
}

// abandon ends a flow that end didn't, such as on a panic or a failure, which are vlatte's answer, not the mock's:
// its stages drop what they hold. A nil w, for a mock without steps, has none.
func (w *topResponseWriter) abandon() {
	if w != nil && !w.ended {
		w.flow.Abort(errAbandoned)
		_ = w.end()
	}
}

// aborted returns the cause of the flow when a stage aborted it, or nil. A nil w, for a mock without steps, has none.
func (w *topResponseWriter) aborted() error {
	if w == nil {
		return nil
	}

	if cause := context.Cause(w.flow.Context()); errors.Is(cause, vlatte.ErrAbort) {
		return cause
	}

	return nil
}

// responseSink ends the flow of a topResponseWriter: it writes to its rw, under its lock.
type responseSink struct{ w *topResponseWriter }

func (s responseSink) Write(p []byte) (int, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()

	return s.w.rw.Write(p)
}

func (s responseSink) Flush() error {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()

	return flushTo(http.NewResponseController(s.w.rw))
}

func (s responseSink) Close() error { return nil }

// answerKey keys the answerWriter of a request in its context.
type answerKey struct{}

// answerWriter keeps the status the client gets, the first final one sent, which the journal, the logs and the
// events tell. A handler's own writer is under it: a reply may flush it, push with it, or take its connection over.
//
// withAnswer puts one outside the decorator and the middlewares, which sees what they send in place of a mock, and
// holds the outcomes of the request until the client has its answer. A handler that can't find it, as under a
// middleware that hides both the request's context and its own writer's, keeps one of its own under the middlewares.
type answerWriter struct {
	rw http.ResponseWriter

	// outer is set for the writer withAnswer puts outside the middlewares.
	outer bool

	mu        sync.Mutex
	status    int
	hijacked  bool
	done      bool
	unwinding bool
	held      []func()
}

// withAnswer serves next with an answerWriter outside it, in the context of the request too, so the outcomes of a
// request tell the status the client got, whatever a middleware or the decorator sends. They run once next returns:
// the client has its answer then, but for net/http's last flush, so a test that checks the journal right after the
// response sees the request.
func withAnswer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := &answerWriter{rw: w, outer: true}
		returned := false

		// A panic that no middleware answers drops the connection, as a runtime.Goexit does.
		defer func() { a.finish(!returned) }()

		next.ServeHTTP(a, r.WithContext(context.WithValue(r.Context(), answerKey{}, a)))
		returned = true
	})
}

// answerOf returns the answerWriter of r, which w writes to: the one withAnswer put outside the middlewares, found in
// r's context or under w, or a new one over w when a middleware hid it. The outer one may be done already, as when a
// timeout of the decorator answered first: the outcomes then run at once, with the status the client got.
func answerOf(r *http.Request, w http.ResponseWriter) *answerWriter {
	if a, ok := r.Context().Value(answerKey{}).(*answerWriter); ok {
		return a
	}

	if a, ok := writerOf[*answerWriter](w); ok {
		return a
	}

	return &answerWriter{rw: w}
}

// writerOf returns the writer of type W that w writes to: w, or one that the Unwrap of a writer over it hands out.
func writerOf[W http.ResponseWriter](w http.ResponseWriter) (W, bool) {
	for {
		if found, ok := w.(W); ok {
			return found, true
		}

		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			var none W
			return none, false
		}

		w = u.Unwrap()
	}
}

func (w *answerWriter) Header() http.Header { return w.rw.Header() }

// WriteHeader implements http.ResponseWriter. An informational status, such as 103 Early Hints, isn't the answer,
// nor is a code net/http refuses.
func (w *answerWriter) WriteHeader(code int) {
	if code == http.StatusSwitchingProtocols || code >= 200 && code <= 999 {
		w.sent(code)
	}

	w.rw.WriteHeader(code)
}

// Write implements http.ResponseWriter: a body without a status answers 200.
func (w *answerWriter) Write(p []byte) (int, error) {
	w.sent(http.StatusOK)
	return w.rw.Write(p)
}

// Flush implements http.Flusher.
func (w *answerWriter) Flush() {
	_ = w.FlushError()
}

// FlushError flushes the response to the client, as http.ResponseController does. A flush sends the status, 200
// unless one was set, when the writer can flush.
func (w *answerWriter) FlushError() error {
	err := http.NewResponseController(w.rw).Flush()
	if err == nil {
		w.sent(http.StatusOK)
	}

	return err
}

// Hijack implements http.Hijacker. What goes on a connection taken over isn't a status the server sends.
func (w *answerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.rw).Hijack()
	if err == nil {
		w.mu.Lock()
		w.hijacked = true
		w.mu.Unlock()
	}

	return conn, rw, err
}

// ReadFrom implements io.ReaderFrom, so copying a body, such as a file's, reaches the writer's own, which may send it
// at once.
func (w *answerWriter) ReadFrom(r io.Reader) (int64, error) {
	var (
		n   int64
		err error
	)

	if rf, ok := w.rw.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(r)
	} else {
		n, err = io.Copy(writerOnly{w.rw}, r)
	}

	if n > 0 {
		w.sent(http.StatusOK)
	}

	return n, err
}

// Push implements http.Pusher, when the writer pushes, such as over HTTP/2.
func (w *answerWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.rw.(http.Pusher); ok {
		return p.Push(target, opts)
	}

	return http.ErrNotSupported
}

// Unwrap returns the response writer, so http.ResponseController reaches it.
func (w *answerWriter) Unwrap() http.ResponseWriter { return w.rw }

// sent keeps code, the status the client gets, unless one was sent, or the connection was taken over: net/http
// sends nothing more on it.
func (w *answerWriter) sent(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.status == 0 && !w.hijacked {
		w.status = code
	}
}

// wroteOn keeps code, the status vlatte wrote itself on the connection it took over, such as a CONNECT's 200: the
// client has it, whatever comes of the connection next.
func (w *answerWriter) wroteOn(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.status = code
}

// state returns the status sent, 0 for none yet, whether the connection was taken over, and whether the handler
// panicked past the middlewares, which drops the connection.
func (w *answerWriter) state() (status int, hijacked, unwinding bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.status, w.hijacked, w.unwinding
}

// then runs f, an outcome of the request w answers, once the client has the answer: when the handler withAnswer
// serves returns. It runs f at once when w is a handler's own, its handler returned, or its connection was taken
// over, as a tunnel's, which stays open: what vlatte wrote on it is the answer.
func (w *answerWriter) then(f func()) {
	w.mu.Lock()

	if !w.outer || w.done || w.hijacked {
		w.mu.Unlock()
		f()

		return
	}

	w.held = append(w.held, f)
	w.mu.Unlock()
}

// finish runs the outcomes then held, once the handler withAnswer serves is done: unwinding when it panicked.
func (w *answerWriter) finish(unwinding bool) {
	w.mu.Lock()
	held := w.held
	w.held, w.done, w.unwinding = nil, true, unwinding
	w.mu.Unlock()

	for _, f := range held {
		f()
	}
}

type mockHandler struct {
	app                   *Server
	lifecycle             mockHTTPLifecycle
	templateDataProviders []TemplateDataProvider
	runner                *vlatte.EventRunner
}

func newMockHandler(
	app *Server,
	lifecycle mockHTTPLifecycle,
	templateDataProviders []TemplateDataProvider,
	eventRunner *vlatte.EventRunner,
) *mockHandler {
	return &mockHandler{app, lifecycle, templateDataProviders, eventRunner}
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.app.config.DebugFn("req", r)

	reqValues, rawBody, parseErr := parseRequest(r, h.app)

	// The mock writes to w, through the middlewares' writers: only a writer of its own goes under them.
	reqValues.answer = answerOf(r, w)
	if !reqValues.answer.outer {
		w = reqValues.answer
	}

	h.debugHeader(w, vlatte.HeaderRequestID, reqValues.ReqID)

	if parseErr != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("request_body_parser: %w", parseErr))
	}

	// net/http removes the temporary files of the request it made, and r may be a copy of it, such as a middleware
	// makes. Forms parse in memory, so this is a safeguard.
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	h.lifecycle.OnRequest(reqValues)

	// A request that ends without an outcome, such as one whose client left, or that panicked, is journaled too, once
	// the client has its answer. A panic goes on to the recovery middleware, which answers it, as it goes on through
	// net/http's.
	defer func() {
		if reqValues.journal == nil {
			return
		}

		// A middleware may end the context it gave once the mock handler returns: whether the client left is told now.
		p, left := recover(), context.Cause(r.Context())
		reqValues.answer.then(func() { reqValues.journal.end(reqValues, p, left) })

		if p != nil {
			panic(p)
		}
	}()

	input := newSelectorInput(r, reqValues, h.app.proxy != nil && (r.Method == http.MethodConnect || r.URL.Host != ""))

	// A recording captures the request as it came, and the answer as the client gets it. A CONNECT opens a tunnel,
	// which answers nothing. A recording with a target sends it what it takes, before any mock.
	var capture *captureWriter
	if rec := h.app.recording.Load(); rec != nil && !rec.stopping.Load() && r.Method != http.MethodConnect && rec.takes(r.Context(), input) {
		capture = rec.capture(w, reqValues, rawBody, parseErr)
		w = capture

		defer capture.end()

		if rec.target != nil {
			h.serveUpstream(w, reqValues, rec.target, capture)
			return
		}
	}

	stored, err := h.app.storage.List(r.Context())
	if err != nil {
		h.onError(w, reqValues, vlatte.FailureMatch, fmt.Errorf("http: listing mocks: %w", err))
		return
	}

	// Near misses go on from where matching stopped, so no matcher runs twice for the request. Only the answer to a
	// request no mock matches, and no proxy takes, lists them. Requests share the room the misses take.
	var misses *vlatte.Misses[*Mock, *selectorInput]
	if !h.app.config.NoNearMisses && !input.ForProxy && (h.app.proxyAll == nil || r.Method == http.MethodConnect) {
		misses, _ = _misses.Get().(*vlatte.Misses[*Mock, *selectorInput])

		defer func() {
			misses.Reset()
			_misses.Put(misses)
		}()
	}

	sub := vlatte.NewExplanation()
	mocks := vlatte.Mocks[*Mock, *selectorInput](stored)
	mock, matched, err := mocks.Find(r.Context(), sub, input, expectationsOf, misses)
	if err != nil {
		h.onError(w, reqValues, vlatte.FailureMatch, fmt.Errorf("http: finding a mock: %w", err))
		return
	}

	// A request no mock matches goes to its host, when the server is a forward proxy: a CONNECT opens a tunnel to it,
	// and a request in absolute form, which names it, gets its answer. Another goes to the target of proxy_all, if set:
	// a CONNECT names no request to send it.
	if !matched {
		switch {
		case input.ForProxy && r.Method == http.MethodConnect:
			h.connect(w, reqValues)
		case input.ForProxy:
			h.serveUpstream(w, reqValues, h.app.proxy, capture)
		case h.app.proxyAll != nil && r.Method != http.MethodConnect:
			h.serveUpstream(w, reqValues, h.app.proxyAll, capture)
		default:
			h.onNoMatches(w, reqValues, misses)
		}

		return
	}

	// Template data providers see the mock that matched.
	reqValues.Mock = mock
	h.app.movedScenarios(reqValues)

	// The hits count this request, which Inc counts once the answer is sent.
	h.debugHeader(w, vlatte.HeaderMatchedMockID, mock.ID())
	h.debugHeader(w, vlatte.HeaderMatchedMockName, mock.Name())
	h.debugHeader(w, vlatte.HeaderMatchedMockHits, strconv.FormatInt(mock.Hits()+1, 10))
	h.debugHeader(w, vlatte.HeaderMatchedMockSource, mock.Origin())

	d := make(map[string]any, len(h.templateDataProviders))
	sdpIn := &TemplateDataProviderInput{r: reqValues}

	for _, sdp := range h.templateDataProviders {
		dd, err := sdp.Data(sdpIn)
		if err != nil {
			h.onError(w, reqValues, vlatte.FailureReply, fmt.Errorf("http: template data provider %s: %w", sdp.Key(), err))
			return
		}

		d[sdp.Key()] = dd
	}

	locals := locals(reqValues, d, viewdata.NewParameters(r.Context(), h.app.params))

	reqValues.Data = locals

	// A client that leaves during the delay gets nothing: the mock doesn't reply, nor count the request. Its scenario
	// moved already, when Find picked it.
	if mock.spec.delay != nil {
		if err := httpx.Wait(r.Context(), mock.spec.delay()); errors.Is(err, context.Canceled) {
			h.lifecycle.OnWarning(reqValues, fmt.Errorf("http: the client left during the delay: %w", context.Cause(r.Context())))
			return
		}
	}

	ctx := r.Context()

	// cut is the cause of a flow a stage aborted. The cut is the mock's answer: the call counts, then the connection
	// is cut, after what was sent, if the answer started.
	var (
		cut     error
		started bool
	)

	// A mock with wire steps answers on its connection, which it takes over, so they see every byte of the answer. It
	// reads a body still the client's first, through the upstream steps: a proxy reply sends what they passed on, and
	// a stage that aborts them cuts the answer before it starts.
	upSteps := mock.UpstreamSteps()

	var upBody *bytes.Reader

	var wire *wireResponseWriter
	if steps := mock.WireSteps(); len(steps) > 0 {
		var err error
		if wire, err = h.takeOver(ctx, w, reqValues, steps, upSteps); err != nil && !errors.Is(err, vlatte.ErrAbort) {
			// A connection the server's close took over is closed: it can't get an answer.
			if !errors.Is(err, errTakeOverClosed) {
				h.onError(w, reqValues, vlatte.FailureReply, err)
			}

			return
		}

		cut = err
	}

	if wire != nil {
		if upBody = wire.upstream; upBody != nil {
			upSteps = nil
		}

		// A panic is vlatte's answer, without the steps, which the recovery middleware can't write on the connection
		// the mock took over: it is answered here. An abort of the handler cuts the connection.
		defer func() {
			p := recover()
			if err, ok := p.(error); p != nil && (!ok || !errors.Is(err, http.ErrAbortHandler)) {
				wire.abandon()
				h.app.recoverer.Answer(wire, r, p)
				wire.finish(nil)

				return
			}

			wire.finish(errAbandoned)

			if p != nil {
				panic(p)
			}
		}()

		w = wire
		ctx = wire.Context()
	}

	// A failure is vlatte's answer, not the mock's, so it skips the steps of the mock. The flow of the steps drives the
	// reply: a stage that aborts it, such as on a timer, ends a reply that waits.
	rw := w

	var top *topResponseWriter
	if steps := mock.Steps(); len(steps) > 0 && cut == nil {
		top = newTopResponseWriter(ctx, steps, w)
		defer top.abandon()

		w = top
		ctx = top.Context()
	}

	noSwitch := len(mock.spec.mappers) > 0

	// fail answers a failure to serve the mock, which skips its steps, and ends the answer.
	fail := func(err error) {
		top.abandon()
		wire.abandon()
		h.onError(rw, reqValues, vlatte.FailureReply, err)
		wire.finish(nil)
	}

	var res *Response
	if cut == nil {
		res, err = mock.reply.Reply(ctx, ReplyInput{
			RequestValues: reqValues,
			Writer:        w,
			noSwitch:      noSwitch,
			upstream:      upSteps,
			upstreamBody:  upBody,
		})
	}

	if err != nil {
		// A stage that aborted the flow can fail the reply, such as a proxy's waiting on the flow: the cut is the
		// mock's answer.
		cut = cmp.Or(top.aborted(), wire.aborted())
		if cut == nil && (errors.Is(err, vlatte.ErrAbort) || errors.Is(err, vlatte.ErrReset)) {
			cut = err
		}

		if cut == nil {
			fail(fmt.Errorf("http: %w", err))
			return
		}

		// The steps of the body end as for any cut: they drop what they hold.
		if top != nil {
			top.flow.Abort(cut)
			_ = top.end()
			started = top.headerSent
		}
	}

	// Render closes the body it sends. A response that isn't rendered, after a failing mapper or a panic, still
	// closes its body here: a proxy's would keep its connection, and a body file its file. A proxy's keeps what it
	// sends only for what reads it once it is sent.
	defer func() {
		if res != nil && res.BodyCloser != nil {
			res.BodyCloser.Close()
		}
	}()

	if res != nil {
		keepBody(res, h.keepsBody(mock))
	}

	// A reply that writes its own response, such as Echo, returns none: it is the mock's.
	if capture != nil {
		capture.answeredBy(res != nil && res.proxied)
	}

	switch {
	case cut != nil:
		// A cut answer isn't recorded.
	case res != nil && res.upgrade != nil:
		// The answer ends with its head: the steps go on with the connection. A stage that cut the answer cuts the
		// switch.
		if top != nil {
			_ = top.end()
			started = top.headerSent
		}

		if cut = cmp.Or(top.aborted(), wire.aborted()); cut != nil {
			res.upgrade.Close()
			break
		}

		h.debugHeader(rw, vlatte.HeaderProxied, "true")

		pipe, ok := h.switchProtocols(rw, reqValues, res, mock, wire)
		if !ok {
			// A wire stage that cut the 101 cut the switch: the mock answered, so the call counts.
			if cut = wire.aborted(); cut == nil {
				return
			}

			break
		}

		// The connections are piped at once, as long as both sides want, while the call is counted, its callbacks
		// and events run, and it is logged. The handler returns once the pipe is done.
		piped := make(chan struct{})
		go func() {
			defer close(piped)
			pipe()
		}()

		defer func() { <-piped }()
	case res != nil:
		// A streamed body, such as a proxy's, is read whole for mappers.
		if len(mock.spec.mappers) > 0 && len(res.Body) == 0 {
			if err = res.readBody(); err != nil {
				fail(fmt.Errorf("http: reading the response body: %w", err))
				return
			}
		}

		for i, mapper := range mock.spec.mappers {
			if err = mapper(reqValues, res); err != nil {
				fail(fmt.Errorf("http: response mapper at index %d: %w", i, err))
				return
			}
		}

		if res.proxied {
			h.debugHeader(w, vlatte.HeaderProxied, "true")
		}

		// The mock counts as served, whatever happens to the answer once its status and headers are sent.
		cut = h.render(ctx, w, reqValues, res, capture)
		started = true
	default:
		// A reply that writes its own answer, such as Echo, wrote it all: the steps pass on what they hold.
		if top != nil {
			started = top.headerSent

			endErr := top.end()
			if cut = cmp.Or(top.aborted(), wire.aborted()); cut == nil && endErr != nil {
				h.renderFailed(reqValues, false, fmt.Errorf("http: ending the steps: %w", endErr))
			}
		}

		if capture != nil && cut == nil {
			capture.record()
		}
	}

	mock.Inc()

	// run matcher events after we sent the mock.
	// some matcher might need to keep state.
	for i, exp := range mock.spec.expectations {
		if stateful, ok := exp.Matcher.(vlatte.Stateful); ok {
			if err = stateful.SetState(); err != nil {
				h.lifecycle.OnWarning(reqValues,
					fmt.Errorf("http: after mock served event: matcher[%d] %w", i, err))
			}
		}
	}

	// The handler waits for the events, so the response ends after them: a test checks what they did right after it.
	// They run before the request log, so its elapsed time counts them.
	h.runner.OnComplete(
		r.Context(),
		mock,
		func(ctx context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
			// The request's context ends when the client leaves: templates read the parameters with the event's.
			data := *locals
			data.Parameters = viewdata.NewParameters(ctx, h.app.params)
			kit := &Kit{
				engine:    h.app.config.TemplateEngine,
				data:      &data,
				logger:    h.app.logger,
				transport: h.app.webhookTransport(),
				dir:       cfg.Dir,
			}

			return &OnComplete{
				m:         mock,
				req:       reqValues,
				res:       res,
				kit:       kit,
				args:      cfg.Parameters,
				dir:       cfg.Dir,
				logger:    h.app.logger,
				transport: h.app.webhookTransport(),
				timeout:   h.app.config.WebhookTimeout,
			}
		},
		func(err error) { h.lifecycle.OnWarning(reqValues, err) },
	)

	h.lifecycle.OnMatch(reqValues, res)

	switch {
	case wire != nil:
		wire.finish(cut)
	case cut != nil:
		h.cut(rw, r, cut, started)
	}
}

// cut ends the answer as a stage of the mock asked, once the call is counted and logged: with a reset over a
// connection that can take one, else by aborting the handler, which closes an HTTP/1 connection and resets an HTTP/2
// stream. w is the writer under the steps. An answer that started goes out first, as far as it got: neither an abort
// nor a hijack sends what the server buffers. One that didn't sends nothing, not even a status.
func (h *mockHandler) cut(w http.ResponseWriter, r *http.Request, cause error, started bool) {
	if started {
		_ = flushTo(http.NewResponseController(w))
	}

	if errors.Is(cause, vlatte.ErrReset) {
		if conn, err := hijack(w); err == nil {
			resetConn(conn)
			return
		}
	}

	// net/http reads what is left of the request's body before it closes the connection, with no deadline: a client
	// that sends nothing more would hold it. The body closes here, past a deadline, so that read ends at once. The
	// connection ends here, so nothing else reads it.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	_ = r.Body.Close()

	panic(http.ErrAbortHandler)
}

// serveUpstream answers with what upstream answers, the target of a recording or the host of the forward proxy, has
// the capture, if any, record it, and logs it: no mock answers, so no OnMatch does.
func (h *mockHandler) serveUpstream(w http.ResponseWriter, r *RequestValues, upstream Replier, capture *captureWriter) {
	ctx := r.RawRequest.Context()

	res, err := upstream.Reply(ctx, ReplyInput{RequestValues: r, Writer: w})
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: %w", err))
		return
	}

	// Only the log reads the body once it is sent.
	keepBody(res, h.app.config.LogVerbosity >= vlatte.LogBody)

	if capture != nil {
		capture.answeredBy(true)
	}

	h.debugHeader(w, vlatte.HeaderProxied, "true")

	if res.upgrade != nil {
		pipe, ok := h.switchProtocols(w, r, res, nil, nil)
		if !ok {
			return
		}

		h.lifecycle.OnProxied(r, res)
		pipe()

		return
	}

	_ = h.render(ctx, w, r, res, capture)
	h.lifecycle.OnProxied(r, res)
}

// switchProtocols answers with res, the 101 of a target that switched protocols, on the client's connection, which it
// takes over, with the headers set on w, such as the debug headers. It returns the pipe between the client's
// connection and the target's, to run once the call is logged, and false when it failed: before it took the
// connection over, the client gets the server's answer to the failure, and after, the log warns. It returns false
// too when a stage of wire cut the 101, which leaves wire to finish with the cut.
//
// mock, when not nil, is the mock that answered: its steps pass on what the target sends, and its upstream steps what
// the client sends. wire, when not nil, holds the client's connection already: its steps go on with the bytes of the
// connection, the 101 included, after the steps of the mock.
func (h *mockHandler) switchProtocols(w http.ResponseWriter, r *RequestValues, res *Response, mock *Mock, wire *wireResponseWriter) (func(), bool) {
	upstream := res.upgrade

	header := w.Header().Clone()
	maps.Copy(header, res.Header)

	var head bytes.Buffer
	_, _ = fmt.Fprintf(&head, "HTTP/1.1 %d %s\r\n", res.StatusCode, http.StatusText(res.StatusCode))
	_ = header.Write(&head)
	_, _ = head.WriteString("\r\n")

	var (
		client   net.Conn
		ctx      context.Context
		toClient *vlatte.Flow
		done     func()
	)

	if wire != nil {
		// The 101 goes first, through the wire steps: they see every byte of the connection. A stage that cuts it cuts
		// the switch, which the caller finishes as any cut of the answer.
		if _, err := wire.flow.Write(head.Bytes()); err != nil {
			upstream.Close()

			if wire.aborted() == nil {
				h.lifecycle.OnWarning(r, fmt.Errorf("http: answering the switch of protocols: %w", err))
			}

			return nil, false
		}

		var (
			wireFlow  *vlatte.Flow
			closeWire func(cut error)
		)

		client, ctx, wireFlow, closeWire = wire.handOver()

		// The server is closing.
		if !h.app.tunnels.add(upstream) {
			upstream.Close()
			closeWire(nil)

			return nil, false
		}

		// What goes to the client ends with the wire steps, and an abort of theirs cuts it: its flow derives from
		// theirs. What goes the other way, from ctx, outlives the end of what goes to the client.
		toClient = vlatte.NewFlow(wireFlow.Context(), wireFlow, mock.Steps())
		done = func() {
			h.app.tunnels.remove(upstream)
			upstream.Close()
			closeWire(nil)
		}
	} else {
		var err error
		if client, err = hijack(w); err != nil {
			upstream.Close()
			h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: switching protocols needs an HTTP/1.1 connection, "+
				"and a response writer that can hijack it, or that unwraps to one that can: %w", err))

			return nil, false
		}

		// The server is closing.
		if !h.app.tunnels.add(client, upstream) {
			client.Close()
			upstream.Close()

			return nil, false
		}

		// A read of the client's connection, now the mock's, can end the request's context.
		var release context.CancelFunc
		ctx, release = h.app.tunnels.bind(context.WithoutCancel(r.RawRequest.Context()))

		done = func() {
			defer release()

			h.app.tunnels.remove(client, upstream)
			client.Close()
			upstream.Close()
		}

		if _, err := client.Write(head.Bytes()); err != nil {
			done()
			h.lifecycle.OnWarning(r, fmt.Errorf("http: answering the switch of protocols: %w", err))

			return nil, false
		}

		if mock != nil && len(mock.Steps()) > 0 {
			toClient = vlatte.NewFlow(ctx, &vlatte.Sink{W: client}, mock.Steps())
		}
	}

	var toUpstream *vlatte.Flow
	if mock != nil && len(mock.UpstreamSteps()) > 0 {
		toUpstream = vlatte.NewFlow(ctx, &vlatte.Sink{W: upstream}, mock.UpstreamSteps())
	}

	r.answer.wroteOn(res.StatusCode)

	return func() {
		err := pipe(client, upstream, toClient, toUpstream)
		if err != nil {
			h.lifecycle.OnWarning(r, fmt.Errorf("http: connection that switched protocols: %w", err))
		}

		done()
	}, true
}

// connect opens a tunnel to the host of r, a CONNECT no mock matched, and pipes the client's connection to it until
// both sides end, or the server closes. A tunnel that can't open gets the server's answer to a failure.
func (h *mockHandler) connect(w http.ResponseWriter, r *RequestValues) {
	req := r.RawRequest

	if looped(req.Header, h.app.id) {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: %w", errProxyLoop))
		return
	}

	if h.app.proxy.interceptor != nil {
		h.intercept(w, r)
		return
	}

	// The host is dialed before the client's connection is taken over: once it is, w can't answer a failure.
	upstream, err := h.dialHost(req.Context(), req)
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: %w", err))
		return
	}

	client, ok := h.openConnect(w, r)
	if !ok {
		upstream.Close()
		return
	}

	defer h.closeConnect(client)

	h.tunnel(client, upstream, r)
}

// openConnect takes the client's connection over from w, for r, a CONNECT, tracks it, and answers 200 on it. It
// reports false when it can't: before it takes the connection over, the client gets the server's answer to the
// failure. closeConnect ends what it opened.
func (h *mockHandler) openConnect(w http.ResponseWriter, r *RequestValues) (net.Conn, bool) {
	client, err := hijack(w)
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: proxy: a tunnel needs an HTTP/1.1 connection, "+
			"and a response writer that can hijack it, or that unwraps to one that can: %w", err))

		return nil, false
	}

	// The server is closing.
	if !h.app.tunnels.add(client) {
		client.Close()
		return nil, false
	}

	// WriteHeader has no effect on a hijacked connection: the answer goes on the connection itself.
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		h.closeConnect(client)
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: answering the CONNECT to %s: %w", r.RawRequest.Host, err))

		return nil, false
	}

	r.answer.wroteOn(http.StatusOK)

	return client, true
}

// closeConnect closes client, which openConnect opened, and stops tracking it.
func (h *mockHandler) closeConnect(client net.Conn) {
	h.app.tunnels.remove(client)
	client.Close()
}

// dialHost dials the host of req, a CONNECT, within ctx, with the server's ID added to the request's VLatte-Via.
func (h *mockHandler) dialHost(ctx context.Context, req *http.Request) (net.Conn, error) {
	via := append(slices.Clone(req.Header.Values(_viaHeader)), h.app.id)
	return h.app.proxy.dial(ctx, req.Host, via)
}

// tunnel logs r, a CONNECT, as answered, and pipes client, which got its 200, to upstream, the connection to the host,
// until both sides end, or the server closes.
func (h *mockHandler) tunnel(client, upstream net.Conn, r *RequestValues) {
	defer upstream.Close()

	// The server is closing.
	if !h.app.tunnels.add(upstream) {
		return
	}

	defer h.app.tunnels.remove(upstream)

	h.logConnect(r)

	if err := pipe(client, upstream, nil, nil); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: tunnel to %s: %w", r.RawRequest.Host, err))
	}
}

// logConnect logs r, a CONNECT, as answered with 200 by the proxy.
func (h *mockHandler) logConnect(r *RequestValues) {
	opened := newResponse()
	opened.StatusCode = http.StatusOK
	h.lifecycle.OnProxied(r, opened)
}

// keepsBody reports whether something reads the body of the answer of mock once it is sent: its on_complete events,
// callbacks included, or the global ones, or the log.
func (h *mockHandler) keepsBody(mock *Mock) bool {
	return len(mock.Events().OnComplete) > 0 ||
		len(h.runner.Global.OnComplete) > 0 ||
		h.app.config.LogVerbosity >= vlatte.LogBody
}

// debugHeader sets the debug header key to value, unless the server's debug headers are off or value is empty.
func (h *mockHandler) debugHeader(w http.ResponseWriter, key, value string) {
	if h.app.config.NoDebugHeaders || value == "" {
		return
	}

	w.Header().Set(key, value)
}

// render sends res through w, ends the steps of the mock, if any, and has the capture, if any, record the answer once
// the client got it whole. It returns the cause of an abort a stage asked for, or nil: the caller cuts the answer.
// The status and headers are sent by now, so an error can't become the response: renderFailed logs it.
func (h *mockHandler) render(ctx context.Context, w http.ResponseWriter, r *RequestValues, res *Response, capture *captureWriter) error {
	err := res.Render(ctx, w)

	// The steps pass on what they hold before anything reads what the client got.
	if top, ok := w.(*topResponseWriter); ok {
		err = cmp.Or(err, top.end())
	}

	// A stage that aborted the flow, of the body or of the wire, cut the answer: a write it failed is no failure.
	if a, ok := w.(interface{ aborted() error }); ok {
		if cut := a.aborted(); cut != nil {
			return cut
		}
	}

	if errors.Is(err, vlatte.ErrAbort) || errors.Is(err, vlatte.ErrReset) {
		return err
	}

	if err != nil {
		h.renderFailed(r, res.proxied, fmt.Errorf("http: rendering response: %w", err))
		return nil
	}

	if capture != nil {
		capture.record()
	}

	return nil
}

// renderFailed logs err, the failure of an answer whose status and headers are sent: as a warning when the client went
// away. A proxied answer whose body broke off cuts the client's connection, as a proxy does, so the client doesn't
// take part of the target's body for all of it.
func (h *mockHandler) renderFailed(r *RequestValues, proxied bool, err error) {
	switch {
	case r.RawRequest.Context().Err() != nil:
		h.lifecycle.OnWarning(r, err)
	case proxied:
		h.lifecycle.OnError(r, err)
		panic(http.ErrAbortHandler)
	default:
		h.lifecycle.OnError(r, err)
	}
}

// onNoMatches answers a request no mock matched with the mocks that came closest, of misses, unless the server
// reports none: misses is nil then. Its journal entry lists them too.
func (h *mockHandler) onNoMatches(w http.ResponseWriter, r *RequestValues, misses *vlatte.Misses[*Mock, *selectorInput]) {
	defer h.lifecycle.OnNoMatch(r)

	if misses != nil {
		r.nearMisses = misses.NearMisses(r.RawRequest.Context(), describeExpectation)
	}

	buf := bufpool.Get()
	buf.Reset()
	defer bufpool.Put(buf)

	buf.WriteString("REQUEST DID NOT MATCH\n")
	buf.WriteString(r.RawRequest.Method)
	buf.WriteString(" ")
	buf.WriteString(r.URL.String())
	buf.WriteString("\n\n")
	writeNearMisses(buf, r.nearMisses)

	w.Header().Add(HeaderContentType, MIMETextPlainCharsetUTF8)
	w.Header().Set(HeaderVlatteError, vlatte.FailureNoMatch)
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)

	buf.WriteTo(w)
}

// writeNearMisses writes misses to buf, one per line, after a title: the parts of each mock that matched, those that
// differ, and those it didn't check. A line break in a name or a matcher's error is a space: a mock stays a line.
func writeNearMisses(buf *bytes.Buffer, misses []vlatte.NearMiss) {
	if len(misses) == 0 {
		return
	}

	buf.WriteString("CLOSEST MOCKS\n")

	for _, miss := range misses {
		line := "[" + miss.MockID + "]"
		if miss.Name != "" && miss.Name != miss.MockID {
			line += " " + miss.Name
		}

		line += fmt.Sprintf(": %d of %d parts matched", miss.Passed, miss.Total)

		if len(miss.Failed) > 0 {
			line += "; differs in " + strings.Join(miss.Failed, ", ")
		}

		if len(miss.Skipped) > 0 {
			line += "; not checked: " + strings.Join(miss.Skipped, ", ")
		}

		_oneLine.WriteString(buf, line)
		buf.WriteString("\n")
	}
}

// _oneLine turns line breaks into spaces.
var _oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// onError answers a request whose serving failed, with the error status, so a client tells it from a mock's answer.
// failure is a vlatte.Failure* value. The body names the mock that matched, if one did, and the error.
// A replier that wrote its status before failing keeps it: the error is logged anyway. A client that left, which
// cancels the request's context, gets no answer, and the failure is a warning, as for a response the client leaves
// during: nothing failed but the client's wait, such as a proxy's call that its leaving ended.
func (h *mockHandler) onError(w http.ResponseWriter, r *RequestValues, failure string, err error) {
	if errors.Is(r.RawRequest.Context().Err(), context.Canceled) {
		h.lifecycle.OnWarning(r, err)
		return
	}

	defer h.lifecycle.OnError(r, err)

	buf := bufpool.Get()
	buf.Reset()
	defer bufpool.Put(buf)

	if failure == vlatte.FailureReply {
		buf.WriteString("REPLY FAILED\n")
	} else {
		buf.WriteString("MATCHING FAILED\n")
	}

	buf.WriteString(r.RawRequest.Method)
	buf.WriteString(" ")
	buf.WriteString(r.URL.String())
	buf.WriteString("\n")

	if r.Mock != nil {
		buf.WriteString(r.Mock.String())
		buf.WriteString("\n")

		if origin := r.Mock.Origin(); origin != "" {
			buf.WriteString("Source: ")
			buf.WriteString(origin)
			buf.WriteString("\n")
		}
	}

	buf.WriteString("\n")
	buf.WriteString(err.Error())

	w.Header().Set(HeaderContentType, MIMETextPlainCharsetUTF8)
	w.Header().Set(HeaderVlatteError, failure)
	w.WriteHeader(h.app.config.ErrorStatusCode)

	buf.WriteTo(w)
}

// newSelectorInput returns what the matchers of mocks read of r, whose values rv holds: forProxy tells a request the
// forward proxy may answer.
func newSelectorInput(r *http.Request, rv *RequestValues, forProxy bool) *selectorInput {
	return &selectorInput{
		RawRequest: r,
		URL:        rv.URL,
		Query:      r.URL.Query(),
		Form:       r.PostForm,
		Files:      formFiles(r),
		ParsedBody: rv.ParsedBodyData,
		ForProxy:   forProxy,
	}
}

// expectationsOf returns the matchers of the request part of m.
func expectationsOf(m *Mock) vlatte.Evaluators[*Mock, *selectorInput] {
	return m.spec.expectations
}

// describeExpectation names the part of a request e checks, such as "header Accept": the name of a header, query
// parameter, form field, cookie or path param goes with its kind. Times, and a part of no kind, go by their key. A
// scenario says the state the mock needs and the one it is in.
func describeExpectation(e *expectation) string {
	if sm, ok := e.Matcher.(*matchscenario.Matcher); ok {
		return sm.String()
	}

	scope := matchingScope(e.Target)
	name := scope.FieldName()

	switch {
	case name == "" || scope == matchingScopeRepeat:
		return e.Key
	case e.Key == "":
		return name
	}

	// The key of a method or a scheme is the value it wants, which the part's name says enough of.

	switch scope {
	case matchingScopeHeader, matchingScopeQuery, matchingScopeQueries, matchingScopeFormField, matchingScopeFormFile,
		matchingScopeHeaders, matchingScopeFormFields, matchingScopeCookie, matchingScopePathParam:
		return name + " " + e.Key
	}

	return name
}

func locals(r *RequestValues, provided map[string]any, params *viewdata.Parameters) *viewdata.Data {
	parsed := r.RawRequest.Cookies()
	cookies := make(map[string]*http.Cookie, len(parsed))
	for _, cookie := range parsed {
		cookies[cookie.Name] = cookie
	}

	body := (*viewdata.Body)(nil)
	if len(r.RawBodyBytes) > 0 {
		body = viewdata.NewBody(r.RawRequest.Method, r.RawBodyBytes, r.ParsedBodyData)
	}

	var pathParams map[string]string
	if r.Mock != nil && r.Mock.spec.pathTemplate != nil {
		pathParams, _ = r.Mock.spec.pathTemplate.params(r.URL.EscapedPath())
	}

	req := &viewdata.Request{
		ID:              r.ReqID,
		Method:          r.RawRequest.Method,
		URL:             r.URL,
		URLPathSegments: r.URLPathSegments,
		PathParams:      pathParams,
		Header:          r.RawRequest.Header.Clone(),
		Cookies:         cookies,
		Body:            body,
		PostForm:        r.RawRequest.PostForm,
		MultipartForm:   r.RawRequest.MultipartForm,
	}

	return &viewdata.Data{
		Request:    req,
		Provided:   provided,
		Parameters: params,
		Server:     &viewdata.Server{URL: r.App.URL()},
		Data:       r.App.Data(),
		Scenarios:  r.App.scenarioStore.View(),
	}
}

// formFiles returns the files of r's multipart form, by field name, or nil when r has none.
func formFiles(r *http.Request) map[string][]*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	return r.MultipartForm.File
}
