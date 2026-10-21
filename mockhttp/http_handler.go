package mockhttp

import (
	"bufio"
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

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

// topResponseWriter passes every write of a reply through the steps of its mock, then to rw.
type topResponseWriter struct {
	ctx   context.Context
	steps []vlatte.Step
	rw    http.ResponseWriter
}

func (w *topResponseWriter) Header() http.Header { return w.rw.Header() }

// WriteHeader sends the status and the headers. Steps can change how many bytes go out, so a Content-Length, such as
// a proxy's target sends, would be wrong: the server measures the body instead, or sends it chunked.
func (w *topResponseWriter) WriteHeader(statusCode int) {
	w.rw.Header().Del(HeaderContentLength)
	w.rw.WriteHeader(statusCode)
}

// Write runs a pipeline of the steps per write, since a pipeline passes one buffer once.
// The steps may change the bytes they pass on, so Write reports len(p) written, as io.Writer requires. The first write
// sends the headers, without a Content-Length, as WriteHeader does.
func (w *topResponseWriter) Write(p []byte) (int, error) {
	w.rw.Header().Del(HeaderContentLength)

	if _, err := vlatte.Flow(w.ctx, &vlatte.Sink{W: w.rw}, w.steps).Next(p); err != nil {
		return 0, err
	}

	return len(p), nil
}

// Unwrap returns the response writer, so http.ResponseController can flush it.
func (w *topResponseWriter) Unwrap() http.ResponseWriter { return w.rw }

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

	// A failure is vlatte's answer, not the mock's, so it skips the steps of the mock.
	rw := w
	if steps := mock.Steps(); len(steps) > 0 {
		w = &topResponseWriter{ctx: r.Context(), steps: steps, rw: w}
	}

	noSwitch := len(mock.Steps()) > 0 || len(mock.spec.mappers) > 0

	res, err := mock.reply.Reply(r.Context(), ReplyInput{RequestValues: reqValues, Writer: w, noSwitch: noSwitch})
	if err != nil {
		h.onError(rw, reqValues, vlatte.FailureReply, fmt.Errorf("http: %w", err))
		return
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
	case res != nil && res.upgrade != nil:
		h.debugHeader(rw, vlatte.ProxiedHeader, "true")

		pipe, ok := h.switchProtocols(rw, reqValues, res)
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
				h.onError(rw, reqValues, vlatte.FailureReply, fmt.Errorf("http: reading the response body: %w", err))
				return
			}
		}

		for i, mapper := range mock.spec.mappers {
			if err = mapper(reqValues, res); err != nil {
				h.onError(rw, reqValues, vlatte.FailureReply, fmt.Errorf("http: response mapper at index %d: %w", i, err))
				return
			}
		}

		if res.proxied {
			h.debugHeader(w, vlatte.ProxiedHeader, "true")
		}

		// The mock counts as served, whatever happens to the answer once its status and headers are sent.
		h.render(w, reqValues, res, capture)
	case capture != nil:
		capture.record()
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
		pipe, ok := h.switchProtocols(w, r, res)
		if !ok {
			return
		}

		h.lifecycle.OnProxied(r, res)
		pipe()

		return
	}

	h.render(w, r, res, capture)
	h.lifecycle.OnProxied(r, res)
}

// switchProtocols answers with res, the 101 of a target that switched protocols, on the client's connection, which it
// takes over, with the headers set on w, such as the debug headers. It returns the pipe between the client's
// connection and the target's, to run once the call is logged, and false when it failed: before it took the
// connection over, the client gets the server's answer to the failure, and after, the log warns.
func (h *mockHandler) switchProtocols(w http.ResponseWriter, r *RequestValues, res *Response) (func(), bool) {
	upstream := res.upgrade

	client, err := hijack(w)
	if err != nil {
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

	done := func() {
		h.app.tunnels.remove(client, upstream)
		client.Close()
		upstream.Close()
	}

	header := w.Header().Clone()
	maps.Copy(header, res.Header)

	out := bufio.NewWriter(client)
	_, _ = fmt.Fprintf(out, "HTTP/1.1 %d %s\r\n", res.StatusCode, http.StatusText(res.StatusCode))
	_ = header.Write(out)
	_, _ = out.WriteString("\r\n")

	if err := out.Flush(); err != nil {
		done()
		h.lifecycle.OnWarning(r, fmt.Errorf("http: answering the switch of protocols: %w", err))

		return nil, false
	}

	return func() {
		defer done()

		if err := pipe(client, upstream); err != nil {
			h.lifecycle.OnWarning(r, fmt.Errorf("http: connection that switched protocols: %w", err))
		}
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

	if err := pipe(client, upstream); err != nil {
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

// render sends res, and has the capture, if any, record it once the client got it whole. The status and headers are
// sent by now, so an error can't become the response: it is logged, as a warning when the client went away. A proxied
// answer whose body broke off cuts the client's connection, as a proxy does, so the client doesn't take part of the
// target's body for all of it.
func (h *mockHandler) render(w http.ResponseWriter, r *RequestValues, res *Response, capture *captureWriter) {
	ctx := r.RawRequest.Context()

	if err := res.Render(ctx, w); err != nil {
		err = fmt.Errorf("http: rendering response: %w", err)

		switch {
		case ctx.Err() != nil:
			h.lifecycle.OnWarning(r, err)
		case res.proxied:
			h.lifecycle.OnError(r, err)
			panic(http.ErrAbortHandler)
		default:
			h.lifecycle.OnError(r, err)
		}

		return
	}

	if capture != nil {
		capture.record()
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
	}
}

// formFiles returns the files of r's multipart form, by field name, or nil when r has none.
func formFiles(r *http.Request) map[string][]*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	return r.MultipartForm.File
}
