package handler

import (
	"context"
)

// MockList is the answer of [Operations.ListMocks]: GET /mocks.
type MockList struct {
	Mocks []MockSummary `json:"mocks"`
}

// ListMocks returns the mocks of the server, enabled or not, in the order they match.
func (o *Operations) ListMocks(ctx context.Context) (MockList, error) {
	return o.mocks.list(ctx)
}

func (m *storeMocks) list(ctx context.Context) (MockList, error) {
	mocks, err := m.mocks.PickAll(ctx)
	if err != nil {
		return MockList{}, err
	}

	list := MockList{Mocks: make([]MockSummary, 0, len(mocks))}
	for _, mock := range mocks {
		list.Mocks = append(list.Mocks, summarize(mock))
	}

	return list, nil
}
