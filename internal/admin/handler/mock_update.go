package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// mockChange is the body of PATCH /mocks/{id}: the state of a mock it changes.
type mockChange struct {
	enabled *bool
}

var _mockChangeSchema = config.NewSchema(
	config.Field[mockChange]{
		Key: "enabled", Kind: config.Bool, Usage: "Whether the mock matches requests.",
		Set: func(c *mockChange, v config.Value) error { c.enabled = new(v.Bool); return nil },
	},
)

// UpdateMock changes the state of the mock of the path's id, and answers it: PATCH /mocks/{id}. A disabled mock
// matches no request, until enabled again.
func UpdateMock[M vlatte.Mock](store vlatte.Store[M]) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		var change mockChange
		if err := decodeBody(_mockChangeSchema, &change, data, format); err != nil {
			fail(w, r, invalid("the body", err))
			return
		}

		mock, err := pick(r, store, r.PathValue("id"))
		if err != nil {
			fail(w, r, err)
			return
		}

		switch {
		case change.enabled == nil:
		case *change.enabled:
			mock.Enable()
		default:
			mock.Disable()
		}

		respond(w, r, http.StatusOK, summarize(mock))
	})
}
