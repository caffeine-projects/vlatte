package handler

import (
	"context"
	"errors"

	"github.com/caffeine-projects/vlatte"
)

// ValidateMock builds the mock of doc, a mock file in format, as [Operations.AddMock] would, and returns its state
// without adding it. A mock with an id in use is a conflict, as it would be for [Operations.AddMock]. It needs a
// server that makes mocks from documents.
func (o *Operations) ValidateMock(ctx context.Context, doc []byte, format string) (MockSummary, error) {
	return o.mocks.validate(ctx, doc, format)
}

func (m *storeMocks) validate(ctx context.Context, doc []byte, format string) (MockSummary, error) {
	mock, err := m.buildMock(doc, format)
	if err != nil {
		return MockSummary{}, err
	}

	defer closeMock(mock)

	_, err = m.mocks.Pick(ctx, mock.ID())

	switch {
	case err == nil:
		return MockSummary{}, storeError(mock.ID(), vlatte.ErrDuplicateID)
	case !errors.Is(err, vlatte.ErrNotFound):
		return MockSummary{}, err
	default:
		return summarize(mock), nil
	}
}
