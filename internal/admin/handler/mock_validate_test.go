package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMock(t *testing.T) {
	// A dry run answers what POST /mocks would, and changes nothing: the mock it built is closed, not added.
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "a mock that would be added", body: `{"id": "new"}`, wantStatus: http.StatusOK},
		{name: "an id in use", body: `{"id": "taken"}`, wantStatus: http.StatusConflict, wantCode: "duplicate_id"},
		{name: "a mock with problems", body: `{"bad": 1}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t, newFake("taken"))
			b := &fakeBuilder{}
			url := serveWith(t, store, b)

			res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/validate", body: tt.body})

			if tt.wantCode == "" {
				require.Equal(t, tt.wantStatus, res.StatusCode, body)
				doc := conforms(t, res, body, "MockSummary")
				require.Equal(t, "new", doc["id"])
			} else {
				problem(t, res, body, tt.wantStatus, tt.wantCode)
			}

			require.Equal(t, []string{"taken"}, ids(t, store), "nothing was added")
			for _, m := range b.built {
				require.Equal(t, int64(1), m.closed.Load(), "the mock built is closed")
			}
		})
	}
}
