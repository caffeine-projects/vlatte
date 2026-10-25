package handler

import (
	"net/http"
)

// RecordingStatus answers the state of the recording: GET /recording.
func RecordingStatus(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		state, err := ops.RecordingStatus()
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, state)
	})
}
