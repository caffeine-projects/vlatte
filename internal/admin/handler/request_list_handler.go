package handler

import (
	"net/http"
	"strconv"
)

// ListRequests answers the newest requests the server served: GET /requests. The limit and before parameters page
// through them, and after, a mark, leaves out those up to it.
func ListRequests(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		limit, before, after, err := pageOf(r)
		if err != nil {
			fail(w, r, err)
			return
		}

		list, err := ops.ListRequests(r.Context(), limit, before, after)
		answerRequests(w, r, list, err)
	})
}

// answerRequests answers list, the requests an operation found, or err, the error it failed with, unless the client
// left: no one reads the answer then.
func answerRequests(w http.ResponseWriter, r *http.Request, list RequestList, err error) {
	switch {
	case err == nil:
		respond(w, r, http.StatusOK, list)
	case r.Context().Err() != nil:
		// The client left: no one reads the answer.
	default:
		fail(w, r, err)
	}
}

// pageOf returns the limit, before and after parameters of r, a page of GET /requests.
func pageOf(r *http.Request) (int, uint64, uint64, error) {
	limit, before, after := _requestsPage, uint64(0), uint64(0)

	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || !pageLimit(int64(n)) {
			return 0, 0, 0, invalidParameter("limit", s, _limitRule)
		}

		limit = n
	}

	if s := r.URL.Query().Get("before"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, 0, invalidParameter("before", s, "the next of a page")
		}

		before = n
	}

	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, 0, invalidParameter("after", s, "the mark of a list")
		}

		after = n
	}

	return limit, before, after, nil
}
