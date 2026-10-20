package mockhttp

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

// topResponseWriter passes every write of a reply through the steps of its mock, then to rw.
type topResponseWriter struct {
	ctx   context.Context
	steps []vlatte.Step
	rw    http.ResponseWriter
}

func (w *topResponseWriter) Header() http.Header        { return w.rw.Header() }
func (w *topResponseWriter) WriteHeader(statusCode int) { w.rw.WriteHeader(statusCode) }

// Write runs a pipeline of the steps per write, since a pipeline passes one buffer once.
// The steps may change the bytes they pass on, so Write reports len(p) written, as io.Writer requires.
func (w *topResponseWriter) Write(p []byte) (int, error) {
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
	// and a request in absolute form, which names it, gets its answer. A request in origin form names no host.
	if !matched {
		switch {
		case h.app.proxy == nil:
			h.onNoMatches(w, reqValues)
		case r.Method == http.MethodConnect:
			h.connect(w, reqValues)
		case r.URL.Host == "":
			h.onNoMatches(w, reqValues)
		default:
			h.serveUpstream(w, reqValues, h.app.proxy, capture)
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

	if mock.spec.delay != nil {
		ctxTimeout, cancel := context.WithTimeout(r.Context(), mock.spec.delay())
		defer cancel()

		<-ctxTimeout.Done()
	}

	// A failure is vlatte's answer, not the mock's, so it skips the steps of the mock.
	rw := w
	if steps := mock.Steps(); len(steps) > 0 {
		w = &topResponseWriter{ctx: r.Context(), steps: steps, rw: w}
	}

	res, err := mock.reply.Reply(r.Context(), ReplyInput{RequestValues: reqValues, Writer: w})
	if err != nil {
		h.onError(rw, reqValues, vlatte.FailureReply, fmt.Errorf("http: %w", err))
		return
	}

	// Render closes the body it sends. A response that isn't rendered, after a failing mapper or a panic, still
	// closes its body here: a proxy's would keep its connection, and a body file its file.
	defer func() {
		if res != nil && res.BodyCloser != nil {
			res.BodyCloser.Close()
		}
	}()

	// A reply that writes its own response, such as Echo, returns none: it is the mock's.
	if capture != nil {
		capture.answeredBy(res != nil && res.proxied)
	}

	if res != nil {
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
	} else if capture != nil {
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

	if capture != nil {
		capture.answeredBy(true)
	}

	h.debugHeader(w, vlatte.ProxiedHeader, "true")
	h.render(w, r, res, capture)
	h.lifecycle.OnProxied(r, res)
}

// connect opens a tunnel to the host of r, a CONNECT no mock matched, and pipes the client's connection to it until
// both sides end, or the server closes. A tunnel that can't open gets the server's answer to a failure.
func (h *mockHandler) connect(w http.ResponseWriter, r *RequestValues) {
	req := r.RawRequest

	if cameBack(req.Header, h.app.id) {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: %w", errProxyLoop))
		return
	}

	// The host is dialed before the client's connection is hijacked: once it is, w can't answer a failure.
	via := append(slices.Clone(req.Header.Values(_viaHeader)), h.app.id)

	upstream, err := h.app.proxy.dial(req.Context(), req.Host, via)
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: %w", err))
		return
	}

	defer upstream.Close()

	client, err := hijack(w)
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: proxy: a tunnel needs an HTTP/1.1 connection, "+
			"and a response writer that can hijack it, or that unwraps to one that can: %w", err))

		return
	}

	defer client.Close()

	// The server is closing.
	if !h.app.tunnels.add(client, upstream) {
		return
	}

	defer h.app.tunnels.remove(client, upstream)

	// WriteHeader has no effect on a hijacked connection: the answer goes on the connection itself.
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: answering the CONNECT to %s: %w", req.Host, err))
		return
	}

	opened := newResponse()
	opened.StatusCode = http.StatusOK
	h.lifecycle.OnProxied(r, opened)

	if err := pipe(client, upstream); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: tunnel to %s: %w", req.Host, err))
	}
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
// A replier that wrote its status before failing keeps it: the error is logged anyway.
func (h *mockHandler) onError(w http.ResponseWriter, r *RequestValues, failure string, err error) {
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
