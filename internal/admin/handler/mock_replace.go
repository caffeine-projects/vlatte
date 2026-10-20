package handler

import (
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// ReplaceMock swaps the mock of the path's id for the mock of the request body, in one step, so a request finds one
// or the other, and answers its state: PUT /mocks/{id}. The body's id must be the path's. The replaced mock is closed.
func ReplaceMock[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		mock, err := buildMock(w, r, build)
		if err != nil {
			fail(w, r, err)
			return
		}

		if mock.ID() != id {
			closeMock(mock)
			fail(w, r, &problemError{
				status: http.StatusBadRequest, code: _codeIDMismatch,
				detail: fmt.Sprintf("the body's id %q isn't the path's %q: a body without an id gets a new one", mock.ID(), id),
			})

			return
		}

		old, err := pick(r, store, id)
		if err != nil {
			closeMock(mock)
			fail(w, r, err)

			return
		}

		if err := store.Replace(r.Context(), []M{old}, mock); err != nil {
			closeMock(mock)
			fail(w, r, storeError(id, err))

			return
		}

		respond(w, r, http.StatusOK, summarize(mock))
	})
}
