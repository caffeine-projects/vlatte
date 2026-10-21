package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// RemoveAllMocks removes every mock, of files and code too, and closes them: DELETE /mocks.
func RemoveAllMocks[M vlatte.Mock](store vlatte.Store[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := store.Clear(r.Context()); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
