package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// GetMock returns the mock of id as a mock file describes it, so it can be sent back. A mock server whose mocks can't
// describe themselves returns a not_supported problem.
func (o *Operations) GetMock(ctx context.Context, id string) (descriptor.Descriptor, error) {
	return o.mocks.get(ctx, id)
}

func (m *storeMocks) get(ctx context.Context, id string) (descriptor.Descriptor, error) {
	mock, err := m.pick(ctx, id)
	if err != nil {
		return descriptor.Descriptor{}, err
	}

	self, ok := any(mock).(descriptor.SelfDescriptive)
	if !ok {
		return descriptor.Descriptor{}, notSupported("describe its mocks")
	}

	return self.Describe(), nil
}

// pick returns the mock with id.
func (m *storeMocks) pick(ctx context.Context, id string) (vlatte.Mock, error) {
	mock, err := m.mocks.Pick(ctx, id)
	if errors.Is(err, vlatte.ErrNotFound) {
		return mock, notFound(id)
	}

	return mock, err
}

func notFound(id string) *problemError {
	return &problemError{status: http.StatusNotFound, code: _codeMockNotFound, detail: fmt.Sprintf("no mock has the id %q", id)}
}

// describeFailed is the problem of the mock of id, whose description failed to encode with err.
func describeFailed(id string, err error) *problemError {
	return &problemError{
		status: http.StatusInternalServerError, code: _codeDescribeFailed,
		detail: fmt.Sprintf("the mock %q can't be described: %v", id, err),
	}
}
