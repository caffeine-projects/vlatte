package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// ReplaceMock swaps the mock of id for the mock of doc, a mock file in format, in one step, so a request finds one or
// the other, and returns its state. The document's id must be id. The replaced mock is closed. It needs a server
// that makes mocks from documents.
func (o *Operations) ReplaceMock(ctx context.Context, id string, doc []byte, format string) (MockSummary, error) {
	return o.mocks.replace(ctx, id, doc, format)
}

func (m *storeMocks) replace(ctx context.Context, id string, doc []byte, format string) (MockSummary, error) {
	mock, err := m.buildMock(doc, format)
	if err != nil {
		return MockSummary{}, err
	}

	if mock.ID() != id {
		closeMock(mock)

		return MockSummary{}, &problemError{
			status: http.StatusBadRequest, code: _codeIDMismatch,
			detail: fmt.Sprintf("the body's id %q isn't the path's %q: a body without an id gets a new one", mock.ID(), id),
		}
	}

	old, err := m.pick(ctx, id)
	if err != nil {
		closeMock(mock)
		return MockSummary{}, err
	}

	if err := m.mocks.Replace(ctx, []vlatte.Mock{old}, mock); err != nil {
		closeMock(mock)
		return MockSummary{}, storeError(id, err)
	}

	return summarize(mock), nil
}
