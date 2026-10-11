package recovery

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"runtime/debug"
)

// New returns a middleware that answers a panic with status and header, and passes it, with its stack, to fn.
func New(fn func(err error), status int, header http.Header) *Recover {
	return &Recover{fn: fn, status: status, header: header}
}

type Recover struct {
	status int
	header http.Header
	fn     func(err error)
}

func (h *Recover) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovery := recover()
			if recovery == nil {
				return
			}

			// net/http aborts a response with it, as the reverse proxy does when the client goes away.
			if err, ok := recovery.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(recovery)
			}

			maps.Copy(w.Header(), h.header)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(h.status)
			_, _ = fmt.Fprintf(w, "PANIC\n%s %s\n\n%v", r.Method, r.URL.String(), recovery)

			h.fn(fmt.Errorf("http: panic serving %s %s: %v\n%s", r.Method, r.URL.String(), recovery, debug.Stack()))
		}()

		next.ServeHTTP(w, r)
	})
}
