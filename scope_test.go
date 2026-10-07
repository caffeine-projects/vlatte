package vlatte_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
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

func TestScope_DeleteOnlyOwnMocks(t *testing.T) {
	// A scope that deleted another scope's mock by ID would break that scope's assertions and cleanup.
	store := vlatte.NewStore[*fakeMock]()
	a, b := newFakeMockWith("a", 0), newFakeMockWith("b", 0)
	require.NoError(t, store.Store(t.Context(), a, b))

	scopeA := vlatte.NewScope([]*fakeMock{a}, store)
	scopeB := vlatte.NewScope([]*fakeMock{b}, store)

	require.NoError(t, scopeA.Delete(t.Context(), "b", "unknown"))

	all, _ := store.PickAll(t.Context())
	require.True(t, sameMocks([]*fakeMock{a, b}, all), "b belongs to another scope")
	require.Same(t, b, scopeB.Mock("b"))

	require.NoError(t, scopeA.Delete(t.Context(), "a"))

	all, _ = store.PickAll(t.Context())
	require.True(t, sameMocks([]*fakeMock{b}, all))
	require.Nil(t, scopeA.Mock("a"))
}

func TestScope_DeleteKeepsANewerMockWithTheID(t *testing.T) {
	// A mock can leave the store without its scope, and its ID can then name a new mock:
	// the new mock isn't the old scope's to delete.
	store := vlatte.NewStore[*fakeMock]()
	old := newFakeMockWith("users", 0)
	require.NoError(t, store.Store(t.Context(), old))

	scope := vlatte.NewScope([]*fakeMock{old}, store)

	require.NoError(t, store.Delete(t.Context(), "users"))

	newer := newFakeMockWith("users", 0)
	require.NoError(t, store.Store(t.Context(), newer))

	require.NoError(t, scope.Clear(t.Context()))

	got, err := store.Pick(t.Context(), "users")
	require.NoError(t, err)
	require.Same(t, newer, got)
	require.Empty(t, scope.Mocks(), "the scope lets go of its own mock either way")
}

func TestScope_DeleteLetsGoOfAMockTheStoreLost(t *testing.T) {
	// A mock that left the store is no longer there to delete; the scope lets go of it all the same.
	store := vlatte.NewStore[*fakeMock]()
	gone, kept := newFakeMockWith("gone", 0), newFakeMockWith("kept", 0)
	require.NoError(t, store.Store(t.Context(), gone, kept))

	scope := vlatte.NewScope([]*fakeMock{gone, kept}, store)
	require.NoError(t, store.Delete(t.Context(), "gone"))

	require.NoError(t, scope.Delete(t.Context(), "gone"))
	require.Nil(t, scope.Mock("gone"))
	require.Same(t, kept, scope.Mock("kept"))

	all, _ := store.PickAll(t.Context())
	require.True(t, sameMocks([]*fakeMock{kept}, all))
}

func TestScope_DeleteReportsStoreErrors(t *testing.T) {
	// A store that can't tell which mock it holds, or can't delete it, leaves the scope as it was.
	for name, store := range map[string]*brokenStore{
		"pick":   newBrokenStore(errors.New("store down"), nil),
		"delete": newBrokenStore(nil, errors.New("store down")),
	} {
		t.Run(name, func(t *testing.T) {
			m := newFakeMockWith("a", 0)
			require.NoError(t, store.Store(t.Context(), m))

			scope := vlatte.NewScope([]*fakeMock{m}, store)

			require.EqualError(t, scope.Clear(t.Context()), "store down")
			require.Same(t, m, scope.Mock("a"))
		})
	}
}

func TestScope_AssertNotCalled(t *testing.T) {
	// Not called means no mock was called: one called mock fails it, and a scope without mocks passes it.
	tests := []struct {
		name   string
		mocks  int
		called int
		want   bool
	}{
		{name: "no mocks", mocks: 0, called: 0, want: true},
		{name: "none called", mocks: 2, called: 0, want: true},
		{name: "one of two called", mocks: 2, called: 1, want: false},
		{name: "all called", mocks: 2, called: 2, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mocks := make([]*fakeMock, 0, tc.mocks)
			for range tc.mocks {
				mocks = append(mocks, newFakeMock())
			}

			for _, m := range mocks[:tc.called] {
				m.hit()
			}

			ft := testmock.NewFakeT()
			scope := vlatte.NewScope(mocks, vlatte.NewStore[*fakeMock]())

			require.Equal(t, tc.want, scope.AssertNotCalled(ft))

			if tc.want {
				ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
				return
			}

			report := errorText(t, ft)
			require.Contains(t, report, strconv.Itoa(tc.called)+" Mocks were called")

			for i, m := range mocks {
				require.Equal(t, i < tc.called, strings.Contains(report, "["+m.ID()+"]"), "only called mocks are named")
			}
		})
	}
}

// errorText returns the message of the one Errorf call ft received.
func errorText(t *testing.T, ft *testmock.FakeT) string {
	t.Helper()

	var calls []mock.Call
	for _, call := range ft.Calls {
		if call.Method == "Errorf" {
			calls = append(calls, call)
		}
	}

	require.Len(t, calls, 1)

	args, _ := calls[0].Arguments.Get(1).([]any)

	return fmt.Sprintf(calls[0].Arguments.String(0), args...)
}
