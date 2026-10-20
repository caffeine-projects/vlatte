package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestStoreFailures(t *testing.T) {
	// A store that fails is the server's problem, not the client's: the answer is a 500 that says what failed. The
	// mocks stay as they were, and a mock the request built is closed, as nothing else would.
	errBroken := errors.New("store: broken")

	tests := []struct {
		name   string
		broken func(s *brokenStore)
		req    request
	}{
		{
			name:   "list",
			broken: func(s *brokenStore) { s.pickAllErr = errBroken },
			req:    request{method: http.MethodGet, url: "/mocks"},
		},
		{
			name:   "add",
			broken: func(s *brokenStore) { s.storeErr = errBroken },
			req:    request{method: http.MethodPost, url: "/mocks", body: `{"id": "new"}`},
		},
		{
			name:   "validate",
			broken: func(s *brokenStore) { s.pickErr = errBroken },
			req:    request{method: http.MethodPost, url: "/mocks/validate", body: `{"id": "new"}`},
		},
		{
			name:   "replace",
			broken: func(s *brokenStore) { s.replaceErr = errBroken },
			req:    request{method: http.MethodPut, url: "/mocks/hello", body: `{"id": "hello"}`},
		},
		{
			name:   "remove",
			broken: func(s *brokenStore) { s.replaceErr = errBroken },
			req:    request{method: http.MethodDelete, url: "/mocks/hello"},
		},
		{
			name:   "remove all",
			broken: func(s *brokenStore) { s.clearErr = errBroken },
			req:    request{method: http.MethodDelete, url: "/mocks"},
		},
		{
			name:   "reset that can't list",
			broken: func(s *brokenStore) { s.pickAllErr = errBroken },
			req:    request{method: http.MethodPost, url: "/reset"},
		},
		{
			name:   "reset that can't remove",
			broken: func(s *brokenStore) { s.replaceErr = errBroken },
			req:    request{method: http.MethodPost, url: "/reset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hello := newFake("hello")
			store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock])}
			require.NoError(t, store.Store(t.Context(), hello))
			tt.broken(store)

			b := &fakeBuilder{}
			req := tt.req
			req.url = serveWith(t, store, b) + req.url

			res, body := send(t, req)

			doc := problem(t, res, body, http.StatusInternalServerError, "internal")
			require.Equal(t, "store: broken", doc["detail"])

			all, err := store.MemoryStore.PickAll(t.Context())
			require.NoError(t, err)
			require.Equal(t, []*fakeMock{hello}, all, "nothing changed")
			require.Zero(t, hello.closed.Load())

			for _, m := range b.built {
				require.Equal(t, int64(1), m.closed.Load(), "the mock the request built is closed")
			}
		})
	}
}
