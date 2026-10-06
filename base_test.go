package vlatte_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

func TestBase_AssertNotCalled(t *testing.T) {
	// A reload leaves an emptied or a new scope behind, and scopes are often partly called:
	// the assertion fails only when a tracked mock was called, and names only those mocks.
	app := vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock]())

	passes := func(t *testing.T) {
		t.Helper()

		ft := testmock.NewFakeT()
		require.True(t, app.AssertNotCalled(ft))
		ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
	}

	passes(t)

	_, err := app.Mock()
	require.NoError(t, err)
	passes(t)

	called, pending := newFakeMockWith("called", 0), newFakeMockWith("pending", 0)
	scope, err := app.Mock(fakeBuilder{mock: called}, fakeBuilder{mock: pending})
	require.NoError(t, err)
	passes(t)

	called.hit()

	ft := testmock.NewFakeT()
	require.False(t, app.AssertNotCalled(ft))

	report := errorText(t, ft)
	require.Contains(t, report, "1 Mock(s) were called")
	require.Contains(t, report, "[called]")
	require.NotContains(t, report, "[pending]")

	require.NoError(t, app.Unmock(t.Context(), scope))
	passes(t)
}

func TestBase_Unmock(t *testing.T) {
	// Unmock deletes the mocks of a scope and stops tracking it; other scopes are left alone.
	store := vlatte.NewStore[*fakeMock]()
	app := vlatte.NewBase[*fakeMock](struct{}{}, store)

	gone, kept := newFakeMockWith("gone", 0), newFakeMockWith("kept", 0)
	scope, err := app.Mock(fakeBuilder{mock: gone})
	require.NoError(t, err)

	_, err = app.Mock(fakeBuilder{mock: kept})
	require.NoError(t, err)

	gone.hit()
	require.NoError(t, app.Unmock(t.Context(), scope))

	all, _ := store.PickAll(t.Context())
	require.True(t, sameMocks([]*fakeMock{kept}, all))
	require.True(t, sameMocks([]*fakeMock{kept}, app.ListPending()))
	require.Empty(t, app.ListCalled(), "the unmocked scope isn't tracked anymore")
	require.Zero(t, app.Hits())

	// The scopes are numbered by the scopes tracked: kept's is now the first.
	ft := testmock.NewFakeT()
	require.False(t, app.AssertCalled(ft))
	require.Contains(t, errorText(t, ft), "Scope 0\n    Mock [kept]")
}

func TestBase_UnmockKeepsTrackingWhatItCantDelete(t *testing.T) {
	// A scope whose mocks are still stored must stay tracked, or they would serve without assertions.
	store := newBrokenStore(errors.New("store down"), nil)
	app := vlatte.NewBase[*fakeMock](struct{}{}, store)

	m := newFakeMockWith("a", 0)
	scope, err := app.Mock(fakeBuilder{mock: m})
	require.NoError(t, err)

	require.EqualError(t, app.Unmock(t.Context(), scope), "store down")
	require.True(t, sameMocks([]*fakeMock{m}, app.ListPending()))
}
