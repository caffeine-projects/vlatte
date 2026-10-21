package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte"
)

// AddMock adds the mock of the request body, a mock file in JSON or YAML, and answers its state: POST /mocks. A mock
// with an id in use is refused, and closed.
func AddMock[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mock, err := buildMock(w, r, build)
		if err != nil {
			fail(w, r, err)
			return
		}

		if err := store.Store(r.Context(), mock); err != nil {
			closeMock(mock)
			fail(w, r, storeError(mock.ID(), err))

			return
		}

		w.Header().Set("Location", "/mocks/"+url.PathEscape(mock.ID()))
		respond(w, r, http.StatusCreated, summarize(mock))
	})
}

// buildMock builds the mock of the request body.
func buildMock[M vlatte.Mock](w http.ResponseWriter, r *http.Request, build vlatte.MockBuilder[M]) (M, error) {
	var zero M

	data, format, err := readBody(w, r)
	if err != nil {
		return zero, err
	}

	mock, err := build.BuildFromBytes(data, format)
	if err != nil {
		return zero, invalid("the mock", err)
	}

	return mock, nil
}

// storeError is the problem of a store that refused the mock with id.
func storeError(id string, err error) error {
	if errors.Is(err, vlatte.ErrDuplicateID) {
		return &problemError{
			status: http.StatusConflict, code: _codeDuplicateID,
			detail: fmt.Sprintf("a mock has the id %q already: replace it with PUT /mocks/%s", id, url.PathEscape(id)),
		}
	}

	return err
}
