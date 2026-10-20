package handler

import (
	"net/http"
)

// health is the answer of GET /health.
type health struct {
	Status string `json:"status"`
	Kind   string `json:"kind,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Health answers that the admin API serves, and what it serves: the kind of the mock server, such as http, and the
// URL it serves its mocks at, which url returns. GET /health.
func Health(kind string, url func() string) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		h := health{Status: "ok", Kind: kind}
		if url != nil {
			h.URL = url()
		}

		respond(w, r, http.StatusOK, h)
	})
}
