package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	_codeRecordingActive      = "recording_active"
	_codeNotRecording         = "not_recording"
	_codeClosing              = "closing"
	_codeRequestNotFound      = "request_not_found"
	_codeInvalidParameter     = "invalid_parameter"
	_codeNotReady             = "not_ready"
	_codeUnknownState         = "unknown_state"
	_codeUnauthorized         = "unauthorized"
	_codeIncompleteJournal    = "incomplete_journal"
	_codeKeyNotFound          = "key_not_found"
)

// problemError is an error of an operation that its caller can fix, which the admin API answers as an RFC 9457
// problem document.
type problemError struct {
	status   int
	code     string
	detail   string
	problems []config.Problem
}

func (e *problemError) Error() string {
	return e.detail
}

// ProblemDocument is an RFC 9457 problem document, as the admin API answers an error. Its type is about:blank, so its
// title is the status's phrase, and code tells problems apart.
type ProblemDocument struct {
	Status   int           `json:"status"`
	Title    string        `json:"title"`
	Code     string        `json:"code"`
	Detail   string        `json:"detail"`
	Instance string        `json:"instance"`
	Problems []ProblemItem `json:"problems,omitempty"`
}

// ProblemItem is one problem of a document: a mock, a body or a mock file.
type ProblemItem struct {
	Code    string `json:"code"`
	Origin  string `json:"origin,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Problem returns the problem document of err, an error of an operation, about instance, what the operation was asked
// for, such as the path of a request. An error the caller can't fix, such as a store that failed, is an internal one,
// of status 500, but for a body over its limit.
func Problem(err error, instance string) ProblemDocument {
	return problemOf(err).document(instance)
}

// fail answers err as a problem document, in the format the request accepts, JSON when it accepts none. Internal
// errors are logged: the client can't fix them.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	p := problemOf(err)
	if p.status >= http.StatusInternalServerError {
		zerolog.Ctx(r.Context()).Error().Str("code", p.code).Str("path", r.URL.Path).Msg("admin: " + p.detail)
	}

	f, ok := problemFormat(r)
	if !ok {
		f = _answerFormats[0]
	}

	body, err := encode(p.document(r.URL.Path), f.format)
	if err != nil {
		http.Error(w, p.detail, p.status)
		return
	}

	write(w, p.status, f.problemType, body)
}

// problemOf returns the problem of err: err itself when it is a *problemError, or else an internal one, but for a body
// too large.
func problemOf(err error) *problemError {
	if p, ok := errors.AsType[*problemError](err); ok {
		return p
	}

	if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &problemError{
			status: http.StatusRequestEntityTooLarge, code: _codeBodyTooLarge,
			detail: fmt.Sprintf("the body is over the limit of %d bytes", tooLarge.Limit),
		}
	}

	return &problemError{status: http.StatusInternalServerError, code: _codeInternal, detail: err.Error()}
}

// document returns the problem document of e about instance.
func (e *problemError) document(instance string) ProblemDocument {
	// A request can send bytes that aren't UTF-8, such as in its path, which JSON and YAML can't hold.
	doc := ProblemDocument{
		Status: e.status, Title: http.StatusText(e.status), Code: e.code,
		Detail: validUTF8(e.detail), Instance: validUTF8(instance),
	}
	for _, item := range e.problems {
		item.Key, item.Value, item.Message = validUTF8(item.Key), validUTF8(item.Value), validUTF8(item.Message)
		doc.Problems = append(doc.Problems, ProblemItem(item))
	}

	return doc
}

// validUTF8 returns s with the bytes that aren't UTF-8 replaced.
func validUTF8(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
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
		fail(w, r, notSupported(what))
	})
}

// notSupported is the problem of an operation the mock server can't do, such as "record".
func notSupported(what string) *problemError {
	return &problemError{status: http.StatusNotImplemented, code: _codeNotSupported, detail: "this mock server can't " + what}
}

// Closing answers a request that comes as the admin API closes: no operation starts then.
func Closing() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		fail(w, r, &problemError{
			status: http.StatusServiceUnavailable, code: _codeClosing, detail: "the admin API is closing",
		})
	})
}

// CrossOriginDenied answers a browser's cross-site request that would change the server, or query its journal, which
// http.CrossOriginProtection refused.
func CrossOriginDenied() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, r, &problemError{
			status: http.StatusForbidden, code: _codeCrossOrigin,
			detail: "a cross-origin request can't change the admin API's state, or query it",
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
