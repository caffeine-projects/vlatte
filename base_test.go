package vlatte_test

import (
	"errors"
	"strings"
	"testing"
	"time"

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

func TestBase_Remock(t *testing.T) {
	// A reload swaps the mocks of the files: a request finds the old ones or the new ones, never neither,
	// and a reload that fails leaves the old ones serving, tracked as before.
	notTracked := "server: the scope to replace isn't tracked: it was replaced or unmocked, or is another server's"

	// setup tracks the scope old, with mocks a and b, and then another scope, with mock x.
	setup := func(t *testing.T, store vlatte.Store[*fakeMock]) (*vlatte.Base[*fakeMock, struct{}], *vlatte.Scope[*fakeMock], []*fakeMock) {
		t.Helper()

		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		mocks := []*fakeMock{newFakeMockWith("a", 0), newFakeMockWith("b", 0), newFakeMockWith("x", 0)}

		old, err := app.Mock(fakeBuilder{mock: mocks[0]}, fakeBuilder{mock: mocks[1]})
		require.NoError(t, err)

		_, err = app.Mock(fakeBuilder{mock: mocks[2]})
		require.NoError(t, err)

		return app, old, mocks
	}

	// unchanged checks that the store holds the mocks of setup, and that old still tracks a and b.
	unchanged := func(t *testing.T, app *vlatte.Base[*fakeMock, struct{}], store vlatte.Store[*fakeMock], old *vlatte.Scope[*fakeMock], mocks []*fakeMock) {
		t.Helper()

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks(mocks, all), "the store holds the same mocks")
		require.True(t, sameMocks(mocks[:2], old.Mocks()), "the old scope holds its mocks")
		require.True(t, sameMocks(mocks, app.ListPending()), "the old scope is still tracked")
	}

	t.Run("new mocks take the place of the old ones", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app, old, mocks := setup(t, store)

		a, c := newFakeMockWith("a", 0), newFakeMockWith("c", 0)
		scope, err := app.Remock(t.Context(), old, fakeBuilder{mock: a}, fakeBuilder{mock: c})
		require.NoError(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{a, c, mocks[2]}, all), "an ID of the old mocks is free for the new ones")
		require.True(t, sameMocks([]*fakeMock{a, c}, scope.Mocks()))
		require.Empty(t, old.Mocks())

		// The new scope has the number of the old one in reports.
		ft := testmock.NewFakeT()
		require.False(t, app.AssertCalled(ft))

		first, second, ok := strings.Cut(errorText(t, ft), "Scope 1\n")
		require.True(t, ok)
		require.Contains(t, first, "Scope 0\n")
		require.Contains(t, first, "[a]")
		require.Contains(t, first, "[c]")
		require.Contains(t, second, "[x]")
	})

	t.Run("a failing builder changes nothing", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app, old, mocks := setup(t, store)

		_, err := app.Remock(t.Context(), old,
			fakeBuilder{mock: newFakeMockWith("c", 0)},
			fakeBuilder{err: errors.New("bad file")},
			fakeBuilder{err: errors.New("worse file")},
		)

		require.EqualError(t, err, "server: mock 1: bad file\nserver: mock 2: worse file")
		unchanged(t, app, store, old, mocks)
	})

	t.Run("an ID in use outside the old scope changes nothing", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app, old, mocks := setup(t, store)

		_, err := app.Remock(t.Context(), old, fakeBuilder{mock: newFakeMockWith("c", 0)}, fakeBuilder{mock: newFakeMockWith("x", 0)})

		require.ErrorIs(t, err, vlatte.ErrDuplicateID)
		require.EqualError(t, err, `server: store: mock id already in use: "x"`)
		unchanged(t, app, store, old, mocks)
	})

	t.Run("a store that refuses changes nothing", func(t *testing.T) {
		store := newBrokenStore(nil, nil)
		app, old, mocks := setup(t, store)
		store.replaceErr = errors.New("store down")

		_, err := app.Remock(t.Context(), old, fakeBuilder{mock: newFakeMockWith("c", 0)})

		require.EqualError(t, err, "server: store down")
		unchanged(t, app, store, old, mocks)
	})

	t.Run("a mock the old scope lost stays out", func(t *testing.T) {
		// Evicted behind the scope's back, its ID now names a newer mock, which the old scope doesn't own.
		store := vlatte.NewStore[*fakeMock]()
		app, old, mocks := setup(t, store)
		require.NoError(t, store.Delete(t.Context(), "a"))

		newer, c := newFakeMockWith("a", 0), newFakeMockWith("c", 0)
		_, err := app.Mock(fakeBuilder{mock: newer})
		require.NoError(t, err)

		_, err = app.Remock(t.Context(), old, fakeBuilder{mock: c})
		require.NoError(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{newer, c, mocks[2]}, all))
	})

	t.Run("only a tracked scope is replaced", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app, old, mocks := setup(t, store)

		c := newFakeMockWith("c", 0)
		_, err := app.Remock(t.Context(), old, fakeBuilder{mock: c})
		require.NoError(t, err)

		_, err = app.Remock(t.Context(), old, fakeBuilder{mock: newFakeMockWith("d", 0)})
		require.EqualError(t, err, notTracked, "replaced already")

		stranger := vlatte.NewScope([]*fakeMock{c}, store)
		_, err = app.Remock(t.Context(), stranger, fakeBuilder{mock: newFakeMockWith("d", 0)})
		require.EqualError(t, err, notTracked, "made outside the server")

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{c, mocks[2]}, all))
	})

	t.Run("no old scope adds the mocks", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app, _, mocks := setup(t, store)

		c := newFakeMockWith("c", 0)
		scope, err := app.Remock(t.Context(), nil, fakeBuilder{mock: c})
		require.NoError(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks(append(mocks, c), all))
		require.True(t, sameMocks([]*fakeMock{c}, scope.Mocks()))
	})
}

func TestBase_AMockOutsideEveryScopeGivesWay(t *testing.T) {
	// The admin API stores mocks outside every scope. A mock file, or code, that defines a mock with the same ID takes
	// its place, as a save or a reload of files the admin wrote must: the mock is replaced, not refused. Mocks of other
	// scopes still keep their IDs.
	t.Run("added", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		posted := newFakeMockWith("x", 0)
		require.NoError(t, store.Store(t.Context(), posted))

		x := newFakeMockWith("x", 0)
		scope, err := app.Mock(fakeBuilder{mock: x})
		require.NoError(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{x}, all))
		require.True(t, sameMocks([]*fakeMock{x}, scope.Mocks()))
		require.Equal(t, int64(1), posted.closed.Load(), "it left the store")
	})

	t.Run("remocked", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		old, err := app.Mock(fakeBuilder{mock: newFakeMockWith("a", 0)})
		require.NoError(t, err)

		posted := newFakeMockWith("x", 0)
		require.NoError(t, store.Store(t.Context(), posted))

		a, x := newFakeMockWith("a", 0), newFakeMockWith("x", 0)
		_, err = app.Remock(t.Context(), old, fakeBuilder{mock: a}, fakeBuilder{mock: x})
		require.NoError(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{a, x}, all))
		require.Equal(t, int64(1), posted.closed.Load())
	})

	t.Run("a failing build leaves it", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		posted := newFakeMockWith("x", 0)
		require.NoError(t, store.Store(t.Context(), posted))

		_, err := app.Mock(fakeBuilder{mock: newFakeMockWith("x", 0)}, fakeBuilder{err: errors.New("bad file")})
		require.Error(t, err)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{posted}, all))
		require.Zero(t, posted.closed.Load())
	})

	t.Run("a store that can't tell which mocks give way changes nothing", func(t *testing.T) {
		store := newBrokenStore(nil, nil)
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		posted := newFakeMockWith("x", 0)
		require.NoError(t, store.Store(t.Context(), posted))
		store.pickAllErr = errors.New("store down")

		x := newFakeMockWith("x", 0)
		_, err := app.Mock(fakeBuilder{mock: x})
		require.EqualError(t, err, "server: store down")

		all, _ := store.MemoryStore.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{posted}, all))
		require.Zero(t, posted.closed.Load())
		require.Equal(t, int64(1), x.closed.Load(), "the mock built is closed")
	})

	t.Run("a mock of another scope keeps its ID", func(t *testing.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)
		scoped := newFakeMockWith("x", 0)
		_, err := app.Mock(fakeBuilder{mock: scoped})
		require.NoError(t, err)

		_, err = app.Mock(fakeBuilder{mock: newFakeMockWith("x", 0)})
		require.ErrorIs(t, err, vlatte.ErrDuplicateID)

		all, _ := store.PickAll(t.Context())
		require.True(t, sameMocks([]*fakeMock{scoped}, all))
	})
}

// blockingBuilder builds its mock once release is closed, after telling entered it began.
type blockingBuilder struct {
	mock    *fakeMock
	entered chan struct{}
	release chan struct{}
}

func (b blockingBuilder) Build(struct{}) (*fakeMock, error) {
	close(b.entered)
	<-b.release

	return b.mock, nil
}

func TestBase_DisableWaitsForARemock(t *testing.T) {
	// #44: Disable didn't wait for a Remock, as Enable does: one that came while a Remock built, such as a mock file
	// reload, disabled the mocks being replaced, and the ones swapped in stayed enabled.
	app := vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock]())

	old, err := app.Mock(fakeBuilder{mock: newFakeMockWith("a", 0)})
	require.NoError(t, err)

	swapped := newFakeMockWith("a", 0)
	b := blockingBuilder{mock: swapped, entered: make(chan struct{}), release: make(chan struct{})}

	remocked := make(chan error, 1)
	go func() {
		_, err := app.Remock(t.Context(), old, b)
		remocked <- err
	}()

	<-b.entered

	disabled := make(chan struct{})
	go func() {
		defer close(disabled)
		app.Disable()
	}()

	// A Disable that doesn't wait returns at once. One that waits returns only after the Remock: the test lets it
	// try, then lets the Remock end, and never fails for a Disable that is slow to start.
	select {
	case <-disabled:
	case <-time.After(100 * time.Millisecond):
	}

	close(b.release)
	require.NoError(t, <-remocked)
	<-disabled

	require.False(t, swapped.Enabled(), "the mocks swapped in are disabled too")
}
