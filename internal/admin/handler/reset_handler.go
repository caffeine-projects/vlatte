package handler

import (
	"net/http"
)

// Reset removes the mocks the admin API added, loads the mocks of the server's sources again, its mock files and its
// persistent store, starts every scenario over, and clears the journal: POST /reset. Mocks added in code stay as they
// are. A server without mock files, scenarios, or a journal, skips them. The sources load first: when they fail to,
// nothing changes. The mocks of a persistent store come back from it, as it holds them: reset never deletes what it
// holds.
func Reset(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.Reset(r.Context()); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
