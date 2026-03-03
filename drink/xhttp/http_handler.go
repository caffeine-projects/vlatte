package xhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/httprec"
	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/viewdata"
	"github.com/caffeine-projects/vlatte/filter"
)

var ErrTemplateDataKeyExists = fmt.Errorf("http: template data key already exists")

type topResponseWriter struct {
	next vlatte.Next
	rw   http.ResponseWriter
}

func (w *topResponseWriter) Header() http.Header         { return w.rw.Header() }
func (w *topResponseWriter) Write(p []byte) (int, error) { return w.next.Next(p) }
func (w *topResponseWriter) WriteHeader(statusCode int)  { w.rw.WriteHeader(statusCode) }

type mockHandler struct {
	app                 *HTTPMockApp
	lifecycle           mockHTTPLifecycle
	sketchDataProviders []vlatte.TemplateDataProvider
	runner              *vlatte.EventRunner
}

func newMockHandler(
	app *HTTPMockApp,
	lifecycle mockHTTPLifecycle,
	sketchDataProviders []vlatte.TemplateDataProvider,
	eventRunner *vlatte.EventRunner,
) *mockHandler {
	return &mockHandler{app, lifecycle, sketchDataProviders, eventRunner}
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.app.config.DebugFn("req", r)

	parsedURL, urlSegments := h.parseURL(r)
	parsedBody, rawBody, err := parseRequestBody(h.app, r)
	reqValues := &RequestValues{time.Now(), r, parsedURL, urlSegments, nil, nil, h.app, nil, nil}
	if err != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("request_body_parser: %w", err))
	}

	reqValues.RawBody = rawBody
	reqValues.ParsedBody = parsedBody

	h.lifecycle.OnRequest(reqValues)

	beans, err := h.app.storage.Scoop(r.Context())
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
		return
	}

	results := &vlatte.Results{Buf: make([]string, 0)}
	selectorInput := &HTTPValueSelectorInput{r, parsedURL, r.URL.Query(), r.Form, parsedBody}

	mocks := vlatte.Mocks[*HTTPMock, *HTTPValueSelectorInput](beans)
	mock, matched, err := mocks.Find(r.Context(), selectorInput, func(m *HTTPMock) vlatte.Evaluators[*HTTPMock, *HTTPValueSelectorInput] { return m.expectations })
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
	}

	// no mocks matched with the incoming request
	// lets check if we need to proxy and record this request.
	if !matched {
		if h.app.proxy == nil {
			h.onNoMatches(w, reqValues, results)
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

			h.app.rec.dispatch(r, parsedURL, rawBody, res)
		}

		return
	}

	d := make(map[string]any, len(h.sketchDataProviders))
	sdpIn := &TemplateDataProviderInput{r: reqValues, s: h.app.config.Sketch}

	if len(h.sketchDataProviders) > 0 {
		for _, sdp := range h.sketchDataProviders {
			if _, ok := d[sdp.Key()]; ok {
				h.lifecycle.OnWarning(reqValues, fmt.Errorf("%w: key=%s provider=%T", ErrTemplateDataKeyExists, sdp.Key(), sdp))
			}

			dd, err := sdp.Data(sdpIn)
			if err != nil {
				h.lifecycle.OnError(reqValues, fmt.Errorf("http: error getting sketch data provider. %w", err))
				return
			}

			d[sdp.Key()] = dd
		}
	}

	locals := locals(reqValues, d, mock.Ext(), nil)
	kit := &Kit{template: h.app.config.Sketch, data: locals}

	reqValues.Data = locals
	reqValues.Mock = mock

	if mock.Delay != nil {
		ctxTimeout, cancel := context.WithTimeout(r.Context(), mock.Delay())
		defer cancel()

		<-ctxTimeout.Done()
	}

	if len(mock.Flow()) > 0 {
		w = &topResponseWriter{vlatte.Flow(&vlatte.Sink{W: w}, mock.Flow()), w}
	}

	res, err := mock.Reply.Build(w, reqValues)
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

		if err := res.Render(w); err != nil {
			h.onError(w, reqValues, fmt.Errorf("http: error rendering response. %w", err))
			return
		}
	}

	mock.Inc()

	// run matcher events after we sent the mock.
	// some matcher might need to keep state.
	for i, exp := range mock.expectations {
		if stateful, ok := exp.Matcher.(filter.Stateful); ok {
			if err = stateful.SetState(); err != nil {
				h.lifecycle.OnWarning(reqValues,
					fmt.Errorf("http: after mock served event: matcher[%d] %w", i, err))
			}
		}
	}

	callbackInput := &HTTPCallbackInput{m: mock, req: reqValues, res: res}
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
			return &HTTPOnComplete{m: mock, req: reqValues, res: res, kit: kit, args: cfg.Parameters}
		},
	); err != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("on_complete: %w", err))
	}

	h.lifecycle.OnMatch(reqValues, res)

	// when proxy is enabled, recording happens some steps before
	// so, we need to record here only if proxy is disabled.
	if h.app.IsRecording() && h.app.config.Proxy == nil {
		h.app.rec.dispatch(r, parsedURL, rawBody, res)
	}
}

func (h *mockHandler) onNoMatches(w http.ResponseWriter, r *RequestValues, desc *vlatte.Results) {
	defer h.lifecycle.OnNoMatch(r, desc)

	buf := bufpool.Get()
	buf.Reset()
	defer bufpool.Put(buf)

	buf.WriteString("REQUEST DID NOT MATCH\n")
	buf.WriteString(r.RawRequest.Method)
	buf.WriteString(" ")
	buf.WriteString(r.URL.String())
	buf.WriteString("\n\n")

	w.Header().Add(httpval.HeaderContentType, httpval.MIMETextPlainCharsetUTF8)
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)

	buf.WriteTo(w)
}

func (h *mockHandler) onError(w http.ResponseWriter, r *RequestValues, err error) {
	defer h.lifecycle.OnError(r, err)

	w.Header().Add(httpval.HeaderContentType, httpval.MIMETextPlainCharsetUTF8)
	w.WriteHeader(h.app.config.RequestWasNotMatchedStatusCode)
	w.Write([]byte("ERROR DURING REQUEST MATCHING\n"))
	w.Write([]byte(err.Error()))
}

func (h *mockHandler) parseURL(r *http.Request) (*url.URL, []string) {
	reqPath := r.URL.Path
	if len(r.URL.RawQuery) > 0 {
		reqPath += "?" + r.URL.RawQuery
	}

	parsedURL, _ := url.Parse(h.app.URL() + reqPath)

	return parsedURL, strings.FieldsFunc(reqPath, func(c rune) bool { return c == '/' })
}

func locals(r *RequestValues, provided map[string]any, ext any, data any) *viewdata.Data {
	cookies := make(map[string]*http.Cookie, len(r.RawRequest.Cookies()))
	for _, cookie := range r.RawRequest.Cookies() {
		cookies[cookie.Name] = cookie
	}

	body := (*viewdata.Body)(nil)
	if len(r.RawBody) > 0 {
		body = viewdata.NewBody(r.RawRequest.Method, r.RawBody, r.ParsedBody)
	}

	req := &viewdata.Request{
		Method:          r.RawRequest.Method,
		URL:             r.URL,
		URLPathSegments: r.URLPathSegments,
		Header:          r.RawRequest.Header.Clone(),
		Cookies:         cookies,
		Body:            body,
	}

	return viewdata.New(req, provided, ext, data)
}
