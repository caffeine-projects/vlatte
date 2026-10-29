package handler

import (
	"net/http"
)

// VerifyRequests answers the verdict of the check of the body on the requests of the journal: QUERY /requests/verify.
// A check that fails is a verdict too: the answer is 200 whether it passed or not.
func VerifyRequests(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		verdict, err := ops.VerifyRequests(r.Context(), data, format)

		switch {
		case err == nil:
			respond(w, r, http.StatusOK, verdict)
		case r.Context().Err() != nil:
			// The client left: no one reads the answer.
		default:
			fail(w, r, err)
		}
	})
}
