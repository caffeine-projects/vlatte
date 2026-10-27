package handler

import (
	"net/http"
)

// StartRecording starts a recording with the settings of the request body, those of the record section of the
// server's settings, and answers its state: POST /recording.
func StartRecording(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		state, err := ops.StartRecording(data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		w.Header().Set("Location", "/recording")
		respond(w, r, http.StatusCreated, state)
	})
}
