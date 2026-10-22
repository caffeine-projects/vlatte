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
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

var (
	_ http.ResponseWriter = (*topResponseWriter)(nil)
	_ http.Flusher        = (*topResponseWriter)(nil)
	_ http.Hijacker       = (*topResponseWriter)(nil)
	_ vlatte.Stage        = responseSink{}
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
	h.debugHeader(w, vlatte.RequestIDHeader, reqValues.ReqID)

	if parseErr != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("request_body_parser: %w", parseErr))
	}

	// net/http removes the temporary files of the request it made, and r may be a copy of it, such as a middleware
	// makes. Forms parse in memory, so this is a safeguard.
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	h.lifecycle.OnRequest(reqValues)

	input := &selectorInput{
		RawRequest: r,
		URL:        reqValues.URL,
		Query:      r.URL.Query(),
		Form:       r.PostForm,
		Files:      formFiles(r),
		ParsedBody: reqValues.ParsedBodyData,
		ForProxy:   h.app.proxy != nil && (r.Method == http.MethodConnect || r.URL.Host != ""),
	}

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

	sub := vlatte.NewExplanation()
	mocks := vlatte.Mocks[*Mock, *selectorInput](stored)
	mock, matched, err := mocks.Find(r.Context(), sub, input, func(m *Mock) vlatte.Evaluators[*Mock, *selectorInput] { return m.spec.expectations })
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
			h.onNoMatches(w, reqValues)
		}

		return
	}

	// Template data providers see the mock that matched.
	reqValues.Mock = mock

	// The hits count this request, which Inc counts once the answer is sent.
	h.debugHeader(w, vlatte.MatchedMockIDHeader, mock.ID())
	h.debugHeader(w, vlatte.MatchedMockNameHeader, mock.Name())
	h.debugHeader(w, vlatte.MatchedMockHitsHeader, strconv.FormatInt(mock.Hits()+1, 10))
	h.debugHeader(w, vlatte.MatchedMockSourceHeader, mock.Origin())

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

	// A client that leaves during the delay gets nothing: the mock doesn't reply, nor count the request.
	if mock.spec.delay != nil {
		if err := httpx.Wait(r.Context(), mock.spec.delay()); errors.Is(err, context.Canceled) {
			h.lifecycle.OnWarning(reqValues, fmt.Errorf("http: the client left during the delay: %w", context.Cause(r.Context())))
			return
		}
	}

	ctx := r.Context()

	// A mock with wire steps answers on its connection, which it takes over, so they see every byte of the answer.
	var wire *wireResponseWriter
	if steps := mock.WireSteps(); len(steps) > 0 {
		var err error
		if wire, err = h.takeOver(ctx, w, reqValues, steps); err != nil {
			h.onError(w, reqValues, vlatte.FailureReply, err)
			return
		}

		defer wire.finish(errAbandoned)

		w = wire
		ctx = wire.Context()
	}

	// A failure is vlatte's answer, not the mock's, so it skips the steps of the mock. The flow of the steps drives the
	// reply: a stage that aborts it, such as on a timer, ends a reply that waits.
	rw := w

	var top *topResponseWriter
	if steps := mock.Steps(); len(steps) > 0 {
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

	// cut is the cause of a flow a stage aborted. The cut is the mock's answer: the call counts, then the connection
	// is cut, after what was sent, if the answer started.
	var (
		cut     error
		started bool
	)

	res, err := mock.reply.Reply(ctx, ReplyInput{
		RequestValues: reqValues,
		Writer:        w,
		noSwitch:      noSwitch,
		upstream:      mock.UpstreamSteps(),
	})
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

		h.debugHeader(rw, vlatte.ProxiedHeader, "true")

		pipe, ok := h.switchProtocols(rw, reqValues, res, mock, wire)
		if !ok {
			return
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
			h.debugHeader(w, vlatte.ProxiedHeader, "true")
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

	callbackInput := &CallbackInput{m: mock, req: reqValues, res: res}
	for i, callback := range mock.Callbacks() {
		err = callback(r.Context(), callbackInput)
		if err != nil {
			h.lifecycle.OnWarning(reqValues, fmt.Errorf(
				"callback: error with callback %d %T:\n%w",
				i,
				callback,
				err,
			))
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
			kit := &Kit{engine: h.app.config.TemplateEngine, data: &data}

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
		h.cut(rw, cut, started)
	}
}

// cut ends the answer as a stage of the mock asked, once the call is counted and logged: with a reset over a
// connection that can take one, else by aborting the handler, which closes an HTTP/1 connection and resets an HTTP/2
// stream. w is the writer under the steps. An answer that started goes out first, as far as it got: neither an abort
// nor a hijack sends what the server buffers. One that didn't sends nothing, not even a status.
func (h *mockHandler) cut(w http.ResponseWriter, cause error, started bool) {
	if started {
		_ = flushTo(http.NewResponseController(w))
	}

	if errors.Is(cause, vlatte.ErrReset) {
		if conn, err := hijack(w); err == nil {
			resetConn(conn)
			return
		}
	}

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

	h.debugHeader(w, vlatte.ProxiedHeader, "true")

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
// connection over, the client gets the server's answer to the failure, and after, the log warns.
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
		headOut  io.Writer // where the 101 goes
		toClient *vlatte.Flow
		done     func(cut error)
	)

	if wire != nil {
		var (
			wireFlow  *vlatte.Flow
			closeWire func(cut error)
		)

		client, ctx, wireFlow, closeWire = wire.handOver()
		headOut = wireFlow

		// The server is closing.
		if !h.app.tunnels.add(upstream) {
			upstream.Close()
			closeWire(nil)

			return nil, false
		}

		// What goes to the client ends with the wire steps, and an abort of theirs cuts it: its flow derives from
		// theirs. What goes the other way, from ctx, outlives the end of what goes to the client.
		toClient = vlatte.NewFlow(wireFlow.Context(), wireFlow, mock.Steps())
		done = func(cut error) {
			h.app.tunnels.remove(upstream)
			upstream.Close()
			closeWire(cut)
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

		headOut = client

		// A read of the client's connection, now the mock's, can end the request's context.
		var release context.CancelFunc
		ctx, release = h.app.tunnels.bind(context.WithoutCancel(r.RawRequest.Context()))

		if mock != nil && len(mock.Steps()) > 0 {
			toClient = vlatte.NewFlow(ctx, &vlatte.Sink{W: client}, mock.Steps())
		}

		done = func(cut error) {
			defer release()

			h.app.tunnels.remove(client, upstream)

			if errors.Is(cut, vlatte.ErrReset) {
				resetConn(client)
				resetConn(upstream)

				return
			}

			client.Close()
			upstream.Close()
		}
	}

	var toUpstream *vlatte.Flow
	if mock != nil && len(mock.UpstreamSteps()) > 0 {
		toUpstream = vlatte.NewFlow(ctx, &vlatte.Sink{W: upstream}, mock.UpstreamSteps())
	}

	// The 101 goes first, through the wire steps, if any: they see every byte of the connection.
	if _, err := headOut.Write(head.Bytes()); err != nil {
		// A stage that aborted the 101 cut the switch: the mock answered, so the call counts.
		if cut := context.Cause(ctx); errors.Is(cut, vlatte.ErrAbort) {
			return func() { done(cut) }, true
		}

		done(nil)
		h.lifecycle.OnWarning(r, fmt.Errorf("http: answering the switch of protocols: %w", err))

		return nil, false
	}

	return func() {
		err := pipe(client, upstream, toClient, toUpstream)
		if err != nil {
			h.lifecycle.OnWarning(r, fmt.Errorf("http: connection that switched protocols: %w", err))
		}

		done(nil)
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

// keepsBody reports whether something reads the body of the answer of mock once it is sent: its callbacks, its
// on_complete events or the global ones, or the log.
func (h *mockHandler) keepsBody(mock *Mock) bool {
	return len(mock.Callbacks()) > 0 ||
		len(mock.Events().OnComplete) > 0 ||
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

func (h *mockHandler) onNoMatches(w http.ResponseWriter, r *RequestValues) {
	defer h.lifecycle.OnNoMatch(r)

	buf := bufpool.Get()
	buf.Reset()
	defer bufpool.Put(buf)

	buf.WriteString("REQUEST DID NOT MATCH\n")
	buf.WriteString(r.RawRequest.Method)
	buf.WriteString(" ")
	buf.WriteString(r.URL.String())
	buf.WriteString("\n\n")

	w.Header().Add(HeaderContentType, MIMETextPlainCharsetUTF8)
	w.Header().Set(HeaderVlatteError, vlatte.FailureNoMatch)
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)

	buf.WriteTo(w)
}

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

func locals(r *RequestValues, provided map[string]any, params *viewdata.Parameters) *viewdata.Data {
	cookies := make(map[string]*http.Cookie, len(r.RawRequest.Cookies()))
	for _, cookie := range r.RawRequest.Cookies() {
		cookies[cookie.Name] = cookie
	}

	body := (*viewdata.Body)(nil)
	if len(r.RawBodyBytes) > 0 {
		body = viewdata.NewBody(r.RawRequest.Method, r.RawBodyBytes, r.ParsedBodyData)
	}

	req := &viewdata.Request{
		ID:              r.ReqID,
		Method:          r.RawRequest.Method,
		URL:             r.URL,
		URLPathSegments: r.URLPathSegments,
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
	}
}

// formFiles returns the files of r's multipart form, by field name, or nil when r has none.
func formFiles(r *http.Request) map[string][]*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	return r.MultipartForm.File
}
