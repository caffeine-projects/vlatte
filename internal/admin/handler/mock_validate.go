package handler

import (
	"errors"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// ValidateMock builds the mock of the request body, as POST /mocks would, and answers its state without adding it:
// POST /mocks/validate. A mock with an id in use is a conflict, as it would be for POST /mocks.
func ValidateMock[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mock, err := buildMock(w, r, build)
		if err != nil {
			fail(w, r, err)
			return
		}

		defer closeMock(mock)

		_, err = store.Pick(r.Context(), mock.ID())

		switch {
		case err == nil:
			fail(w, r, storeError(mock.ID(), vlatte.ErrDuplicateID))
		case !errors.Is(err, vlatte.ErrNotFound):
			fail(w, r, err)
		default:
			respond(w, r, http.StatusOK, summarize(mock))
		}
	})
}
