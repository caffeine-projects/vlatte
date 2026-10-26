package handler

import (
	"net/http"
	"net/url"
)

// AddMock adds the mock of the request body, a mock file in JSON or YAML, and answers its state: POST /mocks. A mock
// with an id in use is refused, and closed.
func AddMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		summary, err := ops.AddMock(r.Context(), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		w.Header().Set("Location", "/mocks/"+url.PathEscape(summary.ID))
		respond(w, r, http.StatusCreated, summary)
	})
}
