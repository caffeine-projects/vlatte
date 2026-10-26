package handler

import (
	"net/http"
)

// SaveMocks writes the mocks the admin API added as mock files, and serves them from the files from then on, so they
// outlive the server: POST /mocks/save. A mock that can't be described as a mock file writes no file at all.
func SaveMocks(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		saved, err := ops.SaveMocks(r.Context(), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, saved)
	})
}
