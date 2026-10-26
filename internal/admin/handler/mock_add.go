package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte"
)

// AddMock adds the mock of doc, a mock file in format, and returns its state. A mock with an id in use is refused,
// and closed. It needs a server that makes mocks from documents.
func (o *Operations) AddMock(ctx context.Context, doc []byte, format string) (MockSummary, error) {
	return o.mocks.add(ctx, doc, format)
}

func (m *storeMocks) add(ctx context.Context, doc []byte, format string) (MockSummary, error) {
	mock, err := m.buildMock(doc, format)
	if err != nil {
		return MockSummary{}, err
	}

	if err := m.mocks.Store(ctx, mock); err != nil {
		closeMock(mock)
		return MockSummary{}, storeError(mock.ID(), err)
	}

	return summarize(mock), nil
}

// buildMock builds the mock of doc, a mock file in format.
func (m *storeMocks) buildMock(doc []byte, format string) (vlatte.Mock, error) {
	if !m.mocks.Builds() {
		return nil, notSupported(_lackingBuilder)
	}

	mock, err := m.mocks.Build(doc, format)
	if err != nil {
		return nil, invalid("the mock", err)
	}

	return mock, nil
}

// storeError is the problem of a store that refused the mock with id.
func storeError(id string, err error) error {
	if errors.Is(err, vlatte.ErrDuplicateID) {
		return &problemError{
			status: http.StatusConflict, code: _codeDuplicateID,
			detail: fmt.Sprintf("a mock has the id %q already: replace it with PUT /mocks/%s", id, url.PathEscape(id)),
		}
	}

	return err
}
