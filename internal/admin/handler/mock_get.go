package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// GetMock answers the mock of the path's id as a mock file describes it, so it can be sent back: GET /mocks/{id}.
func GetMock[M vlatte.Mock](store vlatte.Store[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		mock, err := pick(r, store, id)
		if err != nil {
			fail(w, r, err)
			return
		}

		self, ok := any(mock).(descriptor.SelfDescriptive)
		if !ok {
			NotSupported("describe its mocks").ServeHTTP(w, r)
			return
		}

		f, _ := responseFormat(r)

		body, err := encode(self.Describe(), f.format)
		if err != nil {
			fail(w, r, &problemError{
				status: http.StatusInternalServerError, code: _codeDescribeFailed,
				detail: fmt.Sprintf("the mock %q can't be described: %v", id, err),
			})

			return
		}

		write(w, http.StatusOK, f.contentType, body)
	})
}

// pick returns the mock of store with id.
func pick[M vlatte.Mock](r *http.Request, store vlatte.Store[M], id string) (M, error) {
	mock, err := store.Pick(r.Context(), id)
	if errors.Is(err, vlatte.ErrNotFound) {
		return mock, notFound(id)
	}

	return mock, err
}

func notFound(id string) *problemError {
	return &problemError{status: http.StatusNotFound, code: _codeMockNotFound, detail: fmt.Sprintf("no mock has the id %q", id)}
}
