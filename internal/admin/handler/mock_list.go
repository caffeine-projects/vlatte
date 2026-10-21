package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// mockList is the answer of GET /mocks.
type mockList struct {
	Mocks []mockSummary `json:"mocks"`
}

// ListMocks answers the mocks of store, enabled or not, in the order they match: GET /mocks.
func ListMocks[M vlatte.Mock](store vlatte.Store[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mocks, err := store.PickAll(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		list := mockList{Mocks: make([]mockSummary, 0, len(mocks))}
		for _, mock := range mocks {
			list.Mocks = append(list.Mocks, summarize(mock))
		}

		respond(w, r, http.StatusOK, list)
	})
}
