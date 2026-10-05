package vlatte_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

func TestScope(t *testing.T) {
	newTestMock := func() *fakeMock {
		return newFakeMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	repo := vlatte.NewStore[*fakeMock]()
	repo.Store(context.Background(), m1, m2, m3)

	scope := vlatte.NewScope([]*fakeMock{m1, m2, m3}, repo)

	require.Equal(t, 3, len(scope.Mocks()))

	require.Equal(t, m1, scope.Mock(m1.ID()))
	require.Nil(t, scope.Mock("unknown"))

	t.Run("should not return done when there is still pending mocks", func(t *testing.T) {
		ft := testmock.NewFakeT()

		require.False(t, scope.Called())
		require.Equal(t, 3, len(scope.ListPending()))
		require.True(t, scope.Pending())

		scope.AssertCalled(ft)
		ft.AssertNumberOfCalls(t, "Errorf", 1)
	})

	t.Run("should return done when all mocks were called", func(t *testing.T) {
		ft := testmock.NewFakeT()

		m1.hit()

		require.False(t, scope.Called())
		scope.AssertCalled(ft)

		m2.hit()
		m3.hit()

		ft.AssertNumberOfCalls(t, "Errorf", 1)
		require.True(t, scope.AssertCalled(t))
		require.True(t, scope.Called())
		require.True(t, vlatte.Times(t, scope, 3))
		require.False(t, vlatte.Times(ft, scope, 1))
		require.Equal(t, 0, len(scope.ListPending()))
		require.False(t, scope.Pending())
	})

	t.Run("should return total hits from store", func(t *testing.T) {
		require.EqualValues(t, 3, scope.Hits())
		vlatte.Times(t, scope, 3)
	})

	t.Run("should clean all store associated with NewScope when calling .Clear()", func(t *testing.T) {
		err := scope.Clear(context.Background())
		require.NoError(t, err)

		require.Equal(t, 0, len(scope.ListPending()))
		require.True(t, vlatte.Times(t, scope, 0))
		require.False(t, scope.Pending())
	})
}

func TestScopedDelete(t *testing.T) {
	newTestMock := func() *fakeMock {
		return newFakeMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	store := vlatte.NewStore[*fakeMock]()
	err := store.Store(t.Context(), m1, m2, m3)
	require.NoError(t, err)

	scope := vlatte.NewScope([]*fakeMock{m1, m2, m3}, store)

	require.NoError(t, scope.Delete(t.Context(), m1.ID()))
	require.NoError(t, scope.Delete(t.Context(), "unknown"))

	require.Nil(t, scope.Mock(m1.ID()))
}
