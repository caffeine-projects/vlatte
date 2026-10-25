package handler

import (
	"context"
)

// RemoveMock removes the mock of id, and closes it.
func (o *Operations) RemoveMock(ctx context.Context, id string) error {
	return o.mocks.remove(ctx, id)
}

func (m *storeMocks[M]) remove(ctx context.Context, id string) error {
	mock, err := m.pick(ctx, id)
	if err != nil {
		return err
	}

	// Removed as the very mock picked: a mock that took its id since stays.
	return m.store.Replace(ctx, []M{mock})
}
