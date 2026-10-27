package adminapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

var _ Mocks = (*storeMocks[vlatte.Mock])(nil)

// ErrForeignMock is the error of a mock that isn't of the type of the mocks of the server, such as an HTTP mock given
// to a gRPC server.
var ErrForeignMock = errors.New("admin: the mock isn't of the server's type")

// Mocks are the mocks of a mock server, whatever their type: the admin API works on them as vlatte.Mock. Its methods
// are those of [vlatte.Store], and Build builds a mock of the server's type from a mock file.
type Mocks interface {
	Pick(ctx context.Context, id string) (vlatte.Mock, error)
	PickAll(ctx context.Context) ([]vlatte.Mock, error)

	// Store and Replace fail with ErrForeignMock, changing nothing, for a mock of another type than the server's.
	Store(ctx context.Context, mocks ...vlatte.Mock) error
	Replace(ctx context.Context, old []vlatte.Mock, mocks ...vlatte.Mock) error

	Clear(ctx context.Context) error
	Health(ctx context.Context) error

	// Build builds the mock of doc, a mock file in format. It needs a server that builds mocks: see Builds.
	Build(doc []byte, format string) (vlatte.Mock, error)

	// Builds reports whether the server builds mocks from mock files.
	Builds() bool
}

// MocksOf returns the mocks of store, which build makes from mock files. With a nil build, the server makes no mocks
// from mock files.
func MocksOf[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M]) Mocks {
	return &storeMocks[M]{store: store, build: build}
}

// storeMocks are the mocks of a store of one type of mock.
type storeMocks[M vlatte.Mock] struct {
	store vlatte.Store[M]
	build vlatte.MockBuilder[M]
}

func (m *storeMocks[M]) Pick(ctx context.Context, id string) (vlatte.Mock, error) {
	mock, err := m.store.Pick(ctx, id)
	if err != nil {
		return nil, err
	}

	return mock, nil
}

func (m *storeMocks[M]) PickAll(ctx context.Context) ([]vlatte.Mock, error) {
	mocks, err := m.store.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	all := make([]vlatte.Mock, len(mocks))
	for i, mock := range mocks {
		all[i] = mock
	}

	return all, nil
}

func (m *storeMocks[M]) Store(ctx context.Context, mocks ...vlatte.Mock) error {
	typed, err := typedMocks[M](mocks)
	if err != nil {
		return err
	}

	return m.store.Store(ctx, typed...)
}

func (m *storeMocks[M]) Replace(ctx context.Context, old []vlatte.Mock, mocks ...vlatte.Mock) error {
	typedOld, err := typedMocks[M](old)
	if err != nil {
		return err
	}

	typed, err := typedMocks[M](mocks)
	if err != nil {
		return err
	}

	return m.store.Replace(ctx, typedOld, typed...)
}

func (m *storeMocks[M]) Clear(ctx context.Context) error {
	return m.store.Clear(ctx)
}

func (m *storeMocks[M]) Health(ctx context.Context) error {
	return m.store.Health(ctx)
}

func (m *storeMocks[M]) Build(doc []byte, format string) (vlatte.Mock, error) {
	if m.build == nil {
		return nil, errors.ErrUnsupported
	}

	mock, err := m.build.BuildFromBytes(doc, format)
	if err != nil {
		return nil, err
	}

	return mock, nil
}

func (m *storeMocks[M]) Builds() bool {
	return m.build != nil
}

// typedMocks returns mocks as mocks of type M, or ErrForeignMock for one of another type.
func typedMocks[M vlatte.Mock](mocks []vlatte.Mock) ([]M, error) {
	typed := make([]M, len(mocks))
	for i, mock := range mocks {
		t, ok := mock.(M)
		if !ok {
			return nil, fmt.Errorf("%w: %s is a %T", ErrForeignMock, mock.ID(), mock)
		}

		typed[i] = t
	}

	return typed, nil
}
