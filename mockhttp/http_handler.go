package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/httprec"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

var ErrTemplateDataKeyExists = errors.New("http: template data key already exists")

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

	reqValues, rawBody, err := parseRequest(r, h.app)
	if err != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("request_body_parser: %w", err))
	}

	// net/http removes the temporary files of the request it made, and r may be a copy of it, such as chi makes.
	// Forms parse in memory, so this is a safeguard.
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	h.lifecycle.OnRequest(reqValues)

	stored, err := h.app.storage.List(r.Context())
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
		return
	}

	input := &selectorInput{
		RawRequest: r,
		URL:        reqValues.URL,
		Query:      r.URL.Query(),
		Form:       r.PostForm,
		Files:      formFiles(r),
		ParsedBody: reqValues.ParsedBodyData,
	}

	sub := vlatte.NewExplanation()
	mocks := vlatte.Mocks[*Mock, *selectorInput](stored)
	mock, matched, err := mocks.Find(r.Context(), sub, input, func(m *Mock) vlatte.Evaluators[*Mock, *selectorInput] { return m.expectations })
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
	}

	// no mocks matched with the incoming request
	// lets check if we need to proxy and record this request.
	if !matched {
		if h.app.proxy == nil {
			h.onNoMatches(w, reqValues)
			return
		}

		// checking if we should record writes to later record mocks.
		if h.app.IsRecording() {
			w = httprec.Wrap(w)
		}

		// proxying non-matched requests.
		h.app.proxy.ServeHTTP(w, r)

		if h.app.IsRecording() {
			res, err := responseFromWriter(w)
			if err != nil {
				h.onError(w, reqValues, err)
				return
			}

			h.app.rec.dispatch(r, reqValues.URL, rawBody, res)
		}

		return
	}

	d := make(map[string]any, len(h.templateDataProviders))
	sdpIn := &TemplateDataProviderInput{r: reqValues, s: h.app.config.Template}

	if len(h.templateDataProviders) > 0 {
		for _, sdp := range h.templateDataProviders {
			if _, ok := d[sdp.Key()]; ok {
				h.lifecycle.OnWarning(reqValues, fmt.Errorf("%w: key=%s provider=%T", ErrTemplateDataKeyExists, sdp.Key(), sdp))
			}

			dd, err := sdp.Data(sdpIn)
			if err != nil {
				h.lifecycle.OnError(reqValues, fmt.Errorf("http: error getting template data provider. %w", err))
				return
			}

			d[sdp.Key()] = dd
		}
	}

	locals := locals(reqValues, d, viewdata.NewParameters(r.Context(), h.app.params))
	kit := &Kit{template: h.app.config.Template, data: locals}

	reqValues.Data = locals
	reqValues.Mock = mock

	if mock.Delay != nil {
		ctxTimeout, cancel := context.WithTimeout(r.Context(), mock.Delay())
		defer cancel()

		<-ctxTimeout.Done()
	}

	if steps := mock.Steps(); len(steps) > 0 {
		w = &topResponseWriter{ctx: r.Context(), steps: steps, rw: w}
	}

	res, err := mock.Reply.Reply(r.Context(), ReplyInput{RequestValues: reqValues, Writer: w})
	if err != nil {
		h.onError(w, reqValues, fmt.Errorf(
			"http: error building reply for request=%s source=%s. %w",
			reqValues.URL.Path,
			mock.Origin(),
			err,
		))
		return
	}

	if res != nil {
		for i, mapper := range mock.Mappers {
			if err = mapper(reqValues, res); err != nil {
				h.onError(w, reqValues, fmt.Errorf("http: error with response mapper at index %d. %w", i, err))
				return
			}
		}

		// The status and headers are sent by now, so an error can't become the response: it is logged, and the
		// mock counts as served. A client that went away is a warning.
		if err := res.Render(r.Context(), w); err != nil {
			err = fmt.Errorf("http: rendering response: %w", err)
			if r.Context().Err() != nil {
				h.lifecycle.OnWarning(reqValues, err)
			} else {
				h.lifecycle.OnError(reqValues, err)
			}
		}
	}

	mock.Inc()

	// run matcher events after we sent the mock.
	// some matcher might need to keep state.
	for i, exp := range mock.expectations {
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

	if err = h.runner.OnComplete(
		r.Context(),
		mock,
		func(cfg vlatte.EventConfig) vlatte.OnComplete {
			return &OnComplete{m: mock, req: reqValues, res: res, kit: kit, args: cfg.Parameters, dir: cfg.Dir}
		},
	); err != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("on_complete: %w", err))
	}

	h.lifecycle.OnMatch(reqValues, res)

	// when proxy is enabled, recording happens some steps before
	// so, we need to record here only if proxy is disabled.
	if h.app.IsRecording() && h.app.config.Proxy == nil {
		h.app.rec.dispatch(r, reqValues.URL, rawBody, res)
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
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)

	buf.WriteTo(w)
}

func (h *mockHandler) onError(w http.ResponseWriter, r *RequestValues, err error) {
	defer h.lifecycle.OnError(r, err)

	w.Header().Add(HeaderContentType, MIMETextPlainCharsetUTF8)
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)
	w.Write([]byte("ERROR DURING REQUEST MATCHING\n"))
	w.Write([]byte(err.Error()))
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
		Method:          r.RawRequest.Method,
		URL:             r.URL,
		URLPathSegments: r.URLPathSegments,
		Header:          r.RawRequest.Header.Clone(),
		Cookies:         cookies,
		Body:            body,
		PostForm:        r.RawRequest.PostForm,
		MultipartForm:   r.RawRequest.MultipartForm,
	}

	return viewdata.New(req, provided, params)
}

// formFiles returns the files of r's multipart form, by field name, or nil when r has none.
func formFiles(r *http.Request) map[string][]*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	return r.MultipartForm.File
}
