package handler

import (
	"context"
)

// RemoveAllMocks removes every mock, of files and code too, and closes them.
func (o *Operations) RemoveAllMocks(ctx context.Context) error {
	return o.mocks.removeAll(ctx)
}

func (m *storeMocks) removeAll(ctx context.Context) error {
	return m.mocks.Clear(ctx)
}
