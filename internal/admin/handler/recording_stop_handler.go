package handler

import (
	"net/http"
)

// StopRecording stops the recording, waits until it wrote what it took, and answers what it wrote, which the server
// then serves: DELETE /recording. What it couldn't record, or serve, is in the answer's errors: the recording stopped
// all the same.
func StopRecording(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		result, err := ops.StopRecording(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, result)
	})
}
