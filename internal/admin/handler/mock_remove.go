package handler

import (
	"context"

	"github.com/caffeine-projects/vlatte"
)

// RemoveMock removes the mock of id, and closes it.
func (o *Operations) RemoveMock(ctx context.Context, id string) error {
	return o.mocks.remove(ctx, id)
}

func (m *storeMocks) remove(ctx context.Context, id string) error {
	mock, err := m.pick(ctx, id)
	if err != nil {
		return err
	}

	// Removed as the very mock picked: a mock that took its id since stays.
	return m.mocks.Replace(ctx, []vlatte.Mock{mock})
}
