package adminapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Store[*docMock] = (*docStore)(nil)
	_ vlatte.Mock            = (*docMock)(nil)
)

// docMock is a mock as a persistent store rebuilds it from its description: an id and an enabled state.
type docMock struct {
	id      string
	enabled atomic.Bool
	closed  atomic.Int64
}

func newDocMock(id string, enabled bool) *docMock {
	m := &docMock{id: id}
	m.enabled.Store(enabled)

	return m
}

func (m *docMock) ID() string            { return m.id }
func (m *docMock) Name() string          { return "" }
func (m *docMock) Priority() int         { return 0 }
func (m *docMock) Hits() int64           { return 0 }
func (m *docMock) Called() bool          { return false }
func (m *docMock) Enabled() bool         { return m.enabled.Load() }
func (m *docMock) Enable()               { m.enabled.Store(true) }
func (m *docMock) Disable()              { m.enabled.Store(false) }
func (m *docMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *docMock) String() string        { return m.id }

func (m *docMock) Close() error {
	m.closed.Add(1)
	return nil
}

// docStore is a persistent store, such as files or a database: it keeps the descriptions of mocks by ID, here their
// enabled state, and builds new mocks from them each time it is read. Its writes fail with writeErr, and its reads
// and Health with readErr.
type docStore struct {
	mu       sync.Mutex
	docs     map[string]bool
	writeErr error
	readErr  error
}

func newDocStore() *docStore {
	return &docStore{docs: make(map[string]bool)}
}

func (s *docStore) List(ctx context.Context) (iter.Seq[*docMock], error) {
	all, err := s.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	return slices.Values(slices.DeleteFunc(all, func(m *docMock) bool { return !m.Enabled() })), nil
}

func (s *docStore) Store(ctx context.Context, mocks ...*docMock) error {
	return s.Replace(ctx, nil, mocks...)
}

func (s *docStore) Replace(_ context.Context, old []*docMock, mocks ...*docMock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writeErr != nil {
		return s.writeErr
	}

	docs := maps.Clone(s.docs)
	for _, m := range old {
		delete(docs, m.ID())
	}

	for _, m := range mocks {
		if _, taken := docs[m.ID()]; taken {
			return fmt.Errorf("%w: %q", vlatte.ErrDuplicateID, m.ID())
		}

		docs[m.ID()] = m.Enabled()
	}

	s.docs = docs

	return nil
}

func (s *docStore) Pick(_ context.Context, id string) (*docMock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	enabled, ok := s.docs[id]
	if !ok {
		return nil, vlatte.ErrNotFound
	}

	return newDocMock(id, enabled), nil
}

func (s *docStore) PickAll(_ context.Context) ([]*docMock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.readErr != nil {
		return nil, s.readErr
	}

	all := make([]*docMock, 0, len(s.docs))
	for _, id := range slices.Sorted(maps.Keys(s.docs)) {
		all = append(all, newDocMock(id, s.docs[id]))
	}

	return all, nil
}

func (s *docStore) Delete(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writeErr != nil {
		return s.writeErr
	}

	for _, id := range ids {
		delete(s.docs, id)
	}

	return nil
}

func (s *docStore) Health(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readErr
}

func (s *docStore) Clear(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writeErr != nil {
		return s.writeErr
	}

	clear(s.docs)

	return nil
}

// held returns the descriptions s holds: each id and its enabled state.
func (s *docStore) held() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.docs)
}

// servedIDs returns the ids of the mocks store serves, in the order they match.
func servedIDs(t *testing.T, store vlatte.Store[*docMock]) []string {
	t.Helper()

	all, err := store.PickAll(t.Context())
	require.NoError(t, err)

	out := make([]string, 0, len(all))
	for _, m := range all {
		out = append(out, m.ID())
	}

	return out
}

func TestStore_WritesEachChangeThrough(t *testing.T) {
	// Each change of the admin API reaches the persistent store at once, so the mocks it adds outlive the server, and
	// the server serves it at once: a mock file's mock, stored in the server's store directly, never reaches it.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	file := newDocMock("file", true)
	require.NoError(t, serving.Store(ctx, file))

	a, b := newDocMock("a", true), newDocMock("b", true)
	require.NoError(t, s.Store(ctx, a, b))
	require.Equal(t, map[string]bool{"a": true, "b": true}, durable.held())
	require.Equal(t, []string{"file", "a", "b"}, servedIDs(t, s))

	c := newDocMock("c", true)
	require.NoError(t, s.Replace(ctx, []*docMock{a}, c))
	require.Equal(t, map[string]bool{"b": true, "c": true}, durable.held())

	// An edit of a mock's state is stored again: the persistent store keeps it, and the mock its place.
	b.Disable()
	require.NoError(t, s.Replace(ctx, []*docMock{b}, b))
	require.Equal(t, map[string]bool{"b": false, "c": true}, durable.held())
	require.Equal(t, []string{"file", "b", "c"}, servedIDs(t, s))
	require.Zero(t, b.closed.Load(), "b is served still")

	require.NoError(t, s.Delete(ctx, "c"))
	require.Equal(t, map[string]bool{"b": false}, durable.held())

	require.NoError(t, s.Clear(ctx))
	require.Empty(t, durable.held())
	require.Empty(t, servedIDs(t, s))
}

func TestStore_AWriteThePersistentStoreRefusesChangesNothing(t *testing.T) {
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	errDown := errors.New("database down")
	durable.writeErr = errDown

	require.ErrorIs(t, s.Store(ctx, newDocMock("b", true)), errDown)
	require.ErrorIs(t, s.Replace(ctx, []*docMock{a}, newDocMock("c", true)), errDown)
	require.ErrorIs(t, s.Delete(ctx, "a"), errDown)
	require.ErrorIs(t, s.Clear(ctx), errDown)

	require.Equal(t, []string{"a"}, servedIDs(t, s))
	require.Zero(t, a.closed.Load())
}

func TestStore_AWriteTheServerRefusesIsUndone(t *testing.T) {
	// The server refuses an ID a mock file's mock holds: the persistent store, which took it first, doesn't keep it,
	// and gets back the mock it replaced.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	require.NoError(t, serving.Store(ctx, newDocMock("file", true)))

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	require.ErrorIs(t, s.Store(ctx, newDocMock("file", true)), vlatte.ErrDuplicateID)
	require.Equal(t, map[string]bool{"a": true}, durable.held())

	require.ErrorIs(t, s.Replace(ctx, []*docMock{a}, newDocMock("file", true)), vlatte.ErrDuplicateID)
	require.Equal(t, map[string]bool{"a": true}, durable.held())
	require.Equal(t, []string{"file", "a"}, servedIDs(t, s))
}

func TestStore_ReloadServesWhatThePersistentStoreHolds(t *testing.T) {
	// A user, or another server, changes the persistent store directly: a reload serves what it holds now, in place
	// of what the server served from it. The mocks it no longer serves are closed.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	file := newDocMock("file", true)
	require.NoError(t, serving.Store(ctx, file))

	a, b := newDocMock("a", true), newDocMock("b", true)
	require.NoError(t, s.Store(ctx, a, b))

	durable.docs = map[string]bool{"a": false, "c": true}

	require.NoError(t, s.Reload(ctx, nil))

	require.Equal(t, []string{"file", "a", "c"}, servedIDs(t, s))

	reloaded, err := s.Pick(ctx, "a")
	require.NoError(t, err)
	require.NotSame(t, a, reloaded)
	require.False(t, reloaded.Enabled(), "the edit took effect")
	require.Equal(t, int64(1), a.closed.Load())
	require.Equal(t, int64(1), b.closed.Load())
	require.Zero(t, file.closed.Load())

	// The reloaded mocks are the persistent store's: the admin API's changes to them reach it.
	require.NoError(t, s.Replace(ctx, []*docMock{reloaded}))
	require.Equal(t, map[string]bool{"c": true}, durable.held())
}

func TestStore_APersistentMockGivesWayToAMockOfItsID(t *testing.T) {
	// A mock file's mock wins over a mock of the persistent store with its id, as over a mock the admin API adds.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()

	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	s := NewStore[*docMock](serving, durable, &logger)

	file := newDocMock("x", true)
	require.NoError(t, serving.Store(ctx, file))

	durable.docs = map[string]bool{"x": false, "y": true}

	require.NoError(t, s.Reload(ctx, nil))

	got, err := s.Pick(ctx, "x")
	require.NoError(t, err)
	require.Same(t, file, got)
	require.Equal(t, []string{"x", "y"}, servedIDs(t, s))
	require.Contains(t, logs.String(), `"mock":"x"`)
	require.Equal(t, map[string]bool{"x": false, "y": true}, durable.held(), "a reload never writes")
}

func TestStore_AReloadSwapsItsMocksWithTheFilesMocks(t *testing.T) {
	// The server swaps the mocks of its files and those of the persistent store in one step: swap tells it which of
	// the store's mocks to drop and to add, given the files' mocks it adds and those it removes.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	// The last load's mock file mocks: f goes with the reload, and k stays.
	f, k := newDocMock("f", true), newDocMock("k", true)
	require.NoError(t, serving.Store(ctx, f, k))

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	durable.docs = map[string]bool{"a": true, "f": true, "g": true, "k": true}

	g := newDocMock("g", true)
	err := s.Reload(ctx, func(swap vlatte.Swap[*docMock]) error {
		drop, add, err := swap([]*docMock{g}, []*docMock{f})
		require.NoError(t, err)
		require.Equal(t, []*docMock{a}, drop)
		require.Equal(t, []string{"a", "f"}, ids(add), "g and k give way to the file mocks of their ids")

		return serving.Replace(ctx, append([]*docMock{f}, drop...), append([]*docMock{g}, add...)...)
	})
	require.NoError(t, err)

	require.Equal(t, []string{"k", "g", "a", "f"}, servedIDs(t, s))

	// The mocks it serves now are the persistent store's: the admin API's changes to them reach it.
	reloaded, err := s.Pick(ctx, "a")
	require.NoError(t, err)
	require.NoError(t, s.Replace(ctx, []*docMock{reloaded}))
	require.Equal(t, map[string]bool{"f": true, "g": true, "k": true}, durable.held())
}

func TestStore_ASwapTheServerRefusesKeepsItsMocks(t *testing.T) {
	// The server's store refuses the swap: the mocks the server served from the persistent store stay, and stay its.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	durable.docs = map[string]bool{"a": true, "b": true}

	errRefused := errors.New("refused")
	err := s.Reload(ctx, func(swap vlatte.Swap[*docMock]) error {
		_, _, err := swap(nil, nil)
		require.NoError(t, err)

		return errRefused
	})
	require.ErrorIs(t, err, errRefused)

	require.Equal(t, []string{"a"}, servedIDs(t, s))
	require.Zero(t, a.closed.Load())

	require.NoError(t, s.Replace(ctx, []*docMock{a}))
	require.Equal(t, map[string]bool{"b": true}, durable.held(), "a is still the persistent store's")
}

func TestStore_AReloadWhoseOtherSourcesFailChangesNothing(t *testing.T) {
	// The server's mock files fail to load: the persistent store's changes don't take effect either.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	durable.docs = map[string]bool{"b": true}

	errFiles := errors.New("a mock file has problems")
	err := s.Reload(ctx, func(vlatte.Swap[*docMock]) error { return errFiles })

	require.ErrorIs(t, err, errFiles)
	require.Equal(t, []string{"a"}, servedIDs(t, s))
	require.Zero(t, a.closed.Load())
}

func TestStore_AReloadThatFailsChangesNothing(t *testing.T) {
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	a := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, a))

	errDown := errors.New("database down")
	durable.readErr = errDown

	require.ErrorIs(t, s.Reload(ctx, nil), errDown)
	require.Equal(t, []string{"a"}, servedIDs(t, s))
	require.Zero(t, a.closed.Load())
}

func TestStore_IsNotReadyWhileThePersistentStoreIsDown(t *testing.T) {
	// The server serves the mocks of its persistent store: while that store is down, the admin API answers that the
	// server isn't ready.
	ctx := t.Context()
	durable := newDocStore()
	s := NewStore[*docMock](vlatte.NewStore[*docMock](), durable, nil)

	require.NoError(t, s.Health(ctx))

	errDown := errors.New("database down")
	durable.readErr = errDown

	require.ErrorIs(t, s.Health(ctx), errDown)
}

// failingStore is the store of a server whose PickAll or Replace fails with the errors it has.
type failingStore struct {
	*vlatte.MemoryStore[*docMock]

	pickAllErr error
	replaceErr error
}

func (s *failingStore) PickAll(ctx context.Context) ([]*docMock, error) {
	if s.pickAllErr != nil {
		return nil, s.pickAllErr
	}

	return s.MemoryStore.PickAll(ctx)
}

func (s *failingStore) Replace(ctx context.Context, old []*docMock, mocks ...*docMock) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}

	return s.MemoryStore.Replace(ctx, old, mocks...)
}

func TestStore_AReloadTheServerRefusesChangesNothing(t *testing.T) {
	// The mocks a failed reload built never serve: they are closed, and the server serves what it did.
	errBroken := errors.New("store: broken")

	for name, serving := range map[string]*failingStore{
		"list":    {MemoryStore: new(vlatte.MemoryStore[*docMock]), pickAllErr: errBroken},
		"replace": {MemoryStore: new(vlatte.MemoryStore[*docMock]), replaceErr: errBroken},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			durable := newDocStore()
			durable.docs["a"] = true

			s := NewStore[*docMock](serving, durable, nil)

			require.ErrorIs(t, s.Reload(ctx, nil), errBroken)

			all, err := serving.MemoryStore.PickAll(ctx)
			require.NoError(t, err)
			require.Empty(t, all)
		})
	}
}

func TestStore_ListsWhatTheServerServes(t *testing.T) {
	ctx := t.Context()
	serving := vlatte.NewStore[*docMock]()
	s := NewStore[*docMock](serving, newDocStore(), nil)

	require.NoError(t, s.Store(ctx, newDocMock("on", true), newDocMock("off", false)))

	enabled, err := s.List(ctx)
	require.NoError(t, err)

	var listed []string
	for m := range enabled {
		listed = append(listed, m.ID())
	}

	require.Equal(t, []string{"on"}, listed)
}

func TestStore_AMockPickedBeforeAReloadLeavesTheNewerOne(t *testing.T) {
	// A delete or replace of a mock picked before a reload swapped in a newer one with its id, as a race between two
	// admin requests can: the newer mock, and the description the persistent store holds for it, stay.
	ctx := t.Context()
	serving, durable := vlatte.NewStore[*docMock](), newDocStore()
	s := NewStore[*docMock](serving, durable, nil)

	picked := newDocMock("a", true)
	require.NoError(t, s.Store(ctx, picked))
	require.NoError(t, s.Reload(ctx, nil))

	newer, err := s.Pick(ctx, "a")
	require.NoError(t, err)
	require.NotSame(t, picked, newer)

	require.NoError(t, s.Replace(ctx, []*docMock{picked}), "a delete of the picked mock")
	require.ErrorIs(t, s.Replace(ctx, []*docMock{picked}, newDocMock("a", false)), vlatte.ErrDuplicateID,
		"a replace of the picked mock")

	got, err := s.Pick(ctx, "a")
	require.NoError(t, err)
	require.Same(t, newer, got)
	require.Equal(t, map[string]bool{"a": true}, durable.held())
}
