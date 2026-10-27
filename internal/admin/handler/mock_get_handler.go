package handler

import "net/http"

// GetMock answers the mock of the path's id as a mock file describes it, so it can be sent back: GET /mocks/{id}.
func GetMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		desc, err := ops.GetMock(r.Context(), id)
		if err != nil {
			fail(w, r, err)
			return
		}

		f, _ := responseFormat(r)

		body, err := encode(desc, f.format)
		if err != nil {
			fail(w, r, describeFailed(id, err))
			return
		}

		write(w, http.StatusOK, f.contentType, body)
	})
}
