package handler

import (
	"context"

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

// UpdateMock changes the state of the mock of id to the one of doc, a document in format, such as
// {"enabled": false}, and returns it. A disabled mock matches no request, until enabled again. The mock is stored
// again with its new state, which a persistent store keeps: when the store refuses it, the mock keeps the state it
// had.
func (o *Operations) UpdateMock(ctx context.Context, id string, doc []byte, format string) (MockSummary, error) {
	return o.mocks.update(ctx, id, doc, format)
}

func (m *storeMocks) update(ctx context.Context, id string, doc []byte, format string) (MockSummary, error) {
	var change mockChange
	if err := decodeBody(_mockChangeSchema, &change, doc, format); err != nil {
		return MockSummary{}, invalid("the body", err)
	}

	mock, err := m.pick(ctx, id)
	if err != nil {
		return MockSummary{}, err
	}

	if change.enabled != nil && *change.enabled != mock.Enabled() {
		setEnabled(mock, *change.enabled)

		if err := m.mocks.Replace(ctx, []vlatte.Mock{mock}, mock); err != nil {
			setEnabled(mock, !*change.enabled)
			return MockSummary{}, err
		}
	}

	return summarize(mock), nil
}

func setEnabled(mock vlatte.Mock, enabled bool) {
	if enabled {
		mock.Enable()
	} else {
		mock.Disable()
	}
}
