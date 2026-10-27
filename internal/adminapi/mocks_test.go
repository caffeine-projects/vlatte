package adminapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

// plainMock is the least a store needs, of one type of mock.
type plainMock struct {
	id string
}

func (m *plainMock) ID() string            { return m.id }
func (m *plainMock) Name() string          { return "" }
func (m *plainMock) Priority() int         { return 0 }
func (m *plainMock) Hits() int64           { return 0 }
func (m *plainMock) Called() bool          { return false }
func (m *plainMock) Enabled() bool         { return true }
func (m *plainMock) Enable()               {}
func (m *plainMock) Disable()              {}
func (m *plainMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *plainMock) String() string        { return m.id }

// otherMock is a mock of another type, as a mock of another protocol is.
type otherMock struct {
	plainMock
}

func TestMocksOf_WithoutABuilderBuildsNothing(t *testing.T) {
	// A server whose mocks can't come from mock files, as gRPC's can't, says so: the admin API then answers 501.
	mocks := adminapi.MocksOf[*plainMock](vlatte.NewStore[*plainMock](), nil)

	require.False(t, mocks.Builds())

	_, err := mocks.Build([]byte(`{}`), "json")
	require.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestMocksOf_BuildsWithTheServersBuilder(t *testing.T) {
	build := vlatte.MockBuilderFunc[*plainMock](func(content []byte, _ string) (*plainMock, error) {
		return &plainMock{id: string(content)}, nil
	})
	mocks := adminapi.MocksOf[*plainMock](vlatte.NewStore[*plainMock](), build)

	require.True(t, mocks.Builds())

	mock, err := mocks.Build([]byte("hello"), "json")
	require.NoError(t, err)
	require.Equal(t, "hello", mock.ID())
}

func TestMocksOf_AForeignMockChangesNothing(t *testing.T) {
	// The admin API works on vlatte.Mock: a mock of another type than the server's must not reach its store, which
	// holds one type only.
	store := vlatte.NewStore[*plainMock]()
	mocks := adminapi.MocksOf[*plainMock](store, nil)
	hello := &plainMock{id: "hello"}
	require.NoError(t, mocks.Store(t.Context(), hello))

	foreign := &otherMock{plainMock{id: "other"}}

	require.ErrorIs(t, mocks.Store(t.Context(), foreign), adminapi.ErrForeignMock)
	require.ErrorIs(t, mocks.Replace(t.Context(), []vlatte.Mock{hello}, foreign), adminapi.ErrForeignMock)
	require.ErrorIs(t, mocks.Replace(t.Context(), []vlatte.Mock{foreign}), adminapi.ErrForeignMock)

	all, err := mocks.PickAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, []vlatte.Mock{hello}, all)
}

func TestMocksOf_WorksOnTheServersStore(t *testing.T) {
	store := vlatte.NewStore[*plainMock]()
	mocks := adminapi.MocksOf[*plainMock](store, nil)
	hello, bye := &plainMock{id: "hello"}, &plainMock{id: "bye"}

	require.NoError(t, mocks.Store(t.Context(), hello))
	require.NoError(t, mocks.Replace(t.Context(), []vlatte.Mock{hello}, bye))

	picked, err := mocks.Pick(t.Context(), "bye")
	require.NoError(t, err)
	require.Same(t, bye, picked)

	_, err = mocks.Pick(t.Context(), "hello")
	require.ErrorIs(t, err, vlatte.ErrNotFound)

	in, err := store.PickAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*plainMock{bye}, in, "the server's store changed")

	require.NoError(t, mocks.Clear(t.Context()))

	all, err := mocks.PickAll(t.Context())
	require.NoError(t, err)
	require.Empty(t, all)
}

// downStore is a store whose backing store is down.
type downStore struct {
	*vlatte.MemoryStore[*plainMock]

	err error
}

func (s *downStore) Health(context.Context) error {
	return s.err
}

func TestMocksOf_ReportsTheHealthOfTheStore(t *testing.T) {
	// The admin API's GET /readyz asks the mocks: a store that is down must say so through them.
	errDown := errors.New("database down")
	mocks := adminapi.MocksOf[*plainMock](&downStore{MemoryStore: new(vlatte.MemoryStore[*plainMock]), err: errDown}, nil)

	require.ErrorIs(t, mocks.Health(t.Context()), errDown)
}
