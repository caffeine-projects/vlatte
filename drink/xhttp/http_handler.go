package xhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/httprec"
	"github.com/caffeine-projects/vlatte/filter"
)

type topResponseWriter struct {
	next vlatte.Next
	rw   http.ResponseWriter
}

func (w *topResponseWriter) Header() http.Header         { return w.rw.Header() }
func (w *topResponseWriter) Write(p []byte) (int, error) { return w.next.Next(p) }
func (w *topResponseWriter) WriteHeader(statusCode int)  { w.rw.WriteHeader(statusCode) }

type mockHandler struct {
	app       *HTTPMockApp
	lifecycle mockHTTPLifecycle
}

func newMockHandler(app *HTTPMockApp, lifecycle mockHTTPLifecycle) *mockHandler {
	return &mockHandler{app, lifecycle}
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.app.config.DebugFn("req", r)

	parsedURL, urlSegments := h.parseURL(r)
	parsedBody, rawBody, err := parseRequestBody(h.app, r)
	reqValues := &RequestValues{time.Now(), r, parsedURL, urlSegments, nil, nil, h.app, nil}
	if err != nil {
		h.lifecycle.OnWarning(reqValues, fmt.Errorf("request_body_parser: %w", err))
	}

	reqValues.RawBody = rawBody
	reqValues.ParsedBody = parsedBody

	h.lifecycle.OnRequest(reqValues)

	em, err := h.app.storage.Scoop(r.Context())
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
		return
	}

	results := &vlatte.Results{Buf: make([]string, 0)}
	selectorInput := &HTTPValueSelectorInput{r, parsedURL, r.URL.Query(), r.Form, parsedBody}

	mocks := vlatte.Mocks[*HTTPMock, *HTTPValueSelectorInput](em)
	mock, pass, err := mocks.Find(r.Context(), selectorInput, func(m *HTTPMock) vlatte.Evaluators[*HTTPMock, *HTTPValueSelectorInput] { return m.expectations })
	if err != nil {
		h.lifecycle.OnError(reqValues, fmt.Errorf("http: error finding eligible mocks for request. %w", err))
	}

	// no mocks matched with the incoming request
	// lets check if we need to proxy and record this request.
	if !pass {
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

	reqValues.Mock = mock

	if mock.Delay != nil {
		ctxTimeout, cancel := context.WithTimeout(r.Context(), mock.Delay())
		defer cancel()

		<-ctxTimeout.Done()
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

	mock.Inc()

	if res != nil {
		for i, mapper := range mock.Mappers {
			if err = mapper(reqValues, res); err != nil {
				mock.Dec()
				h.onError(w, reqValues, fmt.Errorf("http: error with response mapper at index %d. %w", i, err))
				return
			}
		}

		if len(mock.Flow()) > 0 {
			w = &topResponseWriter{vlatte.Flow(&vlatte.Sink{W: w}, mock.Flow()), w}
		}

		for k, vv := range res.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}

		for k := range res.Trailer {
			w.Header().Add(httpval.HeaderTrailer, k)
		}

		for _, cookie := range res.Cookies {
			http.SetCookie(w, cookie)
		}

		w.WriteHeader(res.StatusCode)

		if len(res.Body) > 0 {
			if len(res.Encoding) > 0 {
				buf := bufpool.Get()
				buf.Reset()

				_, err = res.encode(buf, bytes.NewReader(res.Body))
				if err != nil {
					h.lifecycle.OnError(reqValues, fmt.Errorf("http: error encoding body: %w", err))
					return
				}

				_, err = buf.WriteTo(w)
				if err != nil {
					h.lifecycle.OnError(reqValues, fmt.Errorf("http: error writting body: %w", err))
					return
				}

				bufpool.Put(buf)
			} else {
				w.Write(res.Body)
			}
		} else if res.BodyCloser != nil {
			defer res.BodyCloser.Close()

			if len(res.Encoding) > 0 {
				_, err = res.encode(w, res.BodyCloser)
				if err != nil {
					h.lifecycle.OnError(reqValues, fmt.Errorf("http: error encoding body: %w", err))
					return
				}
			} else {
				_, err = io.Copy(w, res.BodyCloser)
				if err != nil {
					h.lifecycle.OnError(reqValues, fmt.Errorf("http: error writting body: %w", err))
					return
				}
			}
		}

		for k, v := range res.Trailer {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
	}

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

	for i, def := range mock.PostActions() {
		postAction := h.app.config.PostActions[def.Name]
		input := &HTTPPostActionInput{req: reqValues, res: res, m: mock, args: def.Parameters}
		err = postAction.Run(r.Context(), input)
		if err != nil {
			h.lifecycle.OnWarning(reqValues, fmt.Errorf(
				"post_action: error with post action %s(%d) %T:\n%w",
				def.Name,
				i,
				postAction,
				err,
			))
		}
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
