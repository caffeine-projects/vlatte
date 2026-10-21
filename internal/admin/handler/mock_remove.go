package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// RemoveMock removes the mock of the path's id, and closes it: DELETE /mocks/{id}.
func RemoveMock[M vlatte.Mock](store vlatte.Store[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mock, err := pick(r, store, r.PathValue("id"))
		if err != nil {
			fail(w, r, err)
			return
		}

		// Removed as the very mock picked: a mock that took its id since stays.
		if err := store.Replace(r.Context(), []M{mock}); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
