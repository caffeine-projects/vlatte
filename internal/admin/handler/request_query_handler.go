package handler

import (
	"net/http"
)

// QueryRequests answers the requests the query of the body selects, and their count: QUERY /requests. A request
// part, which the server's protocol makes a pattern of, narrows them as a mock matches. An empty body selects them
// all.
func QueryRequests(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		list, err := ops.QueryRequests(r.Context(), data, format)
		answerRequests(w, r, list, err)
	})
}
