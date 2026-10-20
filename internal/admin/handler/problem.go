package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// Codes of the problems the admin API answers with. They are stable, so clients can act on them.
const (
	_codeInvalidBody          = "invalid_body"
	_codeIDMismatch           = "id_mismatch"
	_codeCrossOrigin          = "cross_origin"
	_codeMockNotFound         = "mock_not_found"
	_codeRouteNotFound        = "route_not_found"
	_codeMethodNotAllowed     = "method_not_allowed"
	_codeNotAcceptable        = "not_acceptable"
	_codeDuplicateID          = "duplicate_id"
	_codeBodyTooLarge         = "body_too_large"
	_codeUnsupportedMediaType = "unsupported_media_type"
	_codeDescribeFailed       = "describe_failed"
	_codeLoadFailed           = "load_failed"
	_codeInternal             = "internal"
	_codeNotSupported         = "not_supported"
)

// problemError is an error a handler answers with, as an RFC 9457 problem document.
type problemError struct {
	status   int
	code     string
	detail   string
	problems []config.Problem
}

func (e *problemError) Error() string {
	return e.detail
}

// problemDoc is an RFC 9457 problem document. Its type is about:blank, so its title is the status's phrase, and code
// tells problems apart.
type problemDoc struct {
	Status   int           `json:"status"`
	Title    string        `json:"title"`
	Code     string        `json:"code"`
	Detail   string        `json:"detail"`
	Instance string        `json:"instance"`
	Problems []problemItem `json:"problems,omitempty"`
}

// problemItem is one problem of a document: a mock, a body or a mock file.
type problemItem struct {
	Code    string `json:"code"`
	Origin  string `json:"origin,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// fail answers err as a problem document, in the format the request accepts, JSON when it accepts none. Errors that
// aren't a *problemError are internal ones, but for a body too large. Internal errors are logged: the client can't
// fix them.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	p, ok := errors.AsType[*problemError](err)
	if !ok {
		p = &problemError{status: http.StatusInternalServerError, code: _codeInternal, detail: err.Error()}
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
			p = &problemError{
				status: http.StatusRequestEntityTooLarge, code: _codeBodyTooLarge,
				detail: fmt.Sprintf("the body is over the limit of %d bytes", tooLarge.Limit),
			}
		}
	}

	if p.status >= http.StatusInternalServerError {
		zerolog.Ctx(r.Context()).Error().Str("code", p.code).Str("path", r.URL.Path).Msg("admin: " + p.detail)
	}

	doc := problemDoc{
		Status: p.status, Title: http.StatusText(p.status), Code: p.code, Detail: p.detail, Instance: r.URL.Path,
	}
	for _, item := range p.problems {
		doc.Problems = append(doc.Problems, problemItem(item))
	}

	format, ok := problemFormat(r)
	if !ok {
		format = _formatJSON
	}

	body, err := encode(doc, format)
	if err != nil {
		http.Error(w, p.detail, p.status)
		return
	}

	write(w, p.status, problemType(format), body)
}

// invalid is the problem of a document a request sent, such as "the mock".
func invalid(what string, err error) *problemError {
	problems := configProblems(err)
	if len(problems) == 0 {
		return &problemError{status: http.StatusBadRequest, code: _codeInvalidBody, detail: what + ": " + err.Error()}
	}

	return &problemError{
		status: http.StatusBadRequest, code: _codeInvalidBody, detail: fmt.Sprintf("%s has %s", what, count(len(problems))),
		problems: problems,
	}
}

// configProblems gathers the problems of every *config.Error in the tree of err, such as one per mock file a reload
// read. A *config.Error wraps no error: it ends a chain.
func configProblems(err error) []config.Problem {
	for {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			problems := make([]config.Problem, 0, len(joined.Unwrap()))
			for _, e := range joined.Unwrap() {
				problems = append(problems, configProblems(e)...)
			}

			return problems
		}

		next := errors.Unwrap(err)
		if next == nil {
			if cfgErr, ok := errors.AsType[*config.Error](err); ok {
				return cfgErr.Problems
			}

			return nil
		}

		err = next
	}
}

func count(problems int) string {
	if problems == 1 {
		return "1 problem"
	}

	return fmt.Sprintf("%d problems", problems)
}

// NotSupported answers that the mock server can't do what a route does, such as "record".
func NotSupported(what string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, r, &problemError{
			status: http.StatusNotImplemented, code: _codeNotSupported, detail: "this mock server can't " + what,
		})
	})
}

// CrossOriginDenied answers a browser's cross-site request that would change the server, which
// http.CrossOriginProtection refused.
func CrossOriginDenied() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, r, &problemError{
			status: http.StatusForbidden, code: _codeCrossOrigin,
			detail: "a cross-origin request can't change the admin API's state",
		})
	})
}

// Routing answers, as problem documents, the requests mux has no route for: a path it doesn't have, or a method its
// path doesn't take, with the Allow header mux sets.
func Routing(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern == "" {
			w = &routingWriter{ResponseWriter: w, r: r}
		}

		mux.ServeHTTP(w, r)
	})
}

// routingWriter turns the text answers of a ServeMux that found no route into problem documents.
type routingWriter struct {
	http.ResponseWriter

	r       *http.Request
	replied bool
}

func (rw *routingWriter) WriteHeader(status int) {
	switch status {
	case http.StatusNotFound:
		rw.replied = true
		fail(rw.ResponseWriter, rw.r, &problemError{
			status: status, code: _codeRouteNotFound, detail: fmt.Sprintf("no route has the path %s", rw.r.URL.Path),
		})
	case http.StatusMethodNotAllowed:
		rw.replied = true
		allow := rw.Header().Get("Allow")
		fail(rw.ResponseWriter, rw.r, &problemError{
			status: status, code: _codeMethodNotAllowed,
			detail: fmt.Sprintf("%s doesn't take %s: it takes %s", rw.r.URL.Path, rw.r.Method, allow),
		})
	default:
		rw.ResponseWriter.WriteHeader(status)
	}
}

func (rw *routingWriter) Write(b []byte) (int, error) {
	if rw.replied {
		return len(b), nil
	}

	return rw.ResponseWriter.Write(b)
}
