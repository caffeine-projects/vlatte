package vlatte_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// _storeIDs is a small pool of IDs, so random steps reuse IDs and hit duplicates often.
var _storeIDs = []string{"a", "b", "c", "d", "e", "f"}

func ids(mocks []*fakeMock) []string {
	out := make([]string, 0, len(mocks))
	for _, m := range mocks {
		out = append(out, m.ID())
	}

	return out
}

func TestMemoryStore_RejectsDuplicateIDs(t *testing.T) {
	// IDs name mocks in the admin API and in files, so two mocks can't share one; a refused batch stores nothing.
	ctx := t.Context()
	store := vlatte.NewStore[*fakeMock]()

	require.NoError(t, store.Store(ctx, newFakeMockWith("a", 0)))

	err := store.Store(ctx, newFakeMockWith("b", 0), newFakeMockWith("a", 0))
	require.ErrorIs(t, err, vlatte.ErrDuplicateID)
	require.EqualError(t, err, `store: mock id already in use: "a"`)

	err = store.Store(ctx, newFakeMockWith("c", 0), newFakeMockWith("c", 1))
	require.ErrorIs(t, err, vlatte.ErrDuplicateID, "repeated in the same call")

	all, _ := store.PickAll(ctx)
	require.Equal(t, []string{"a"}, ids(all))
}

func TestMemoryStore_ReplaceIsAtomic(t *testing.T) {
	// A reload swaps the mocks of the files while requests are served: a request that found neither set
	// would get no mock at all.
	ctx := t.Context()
	store := vlatte.NewStore[*fakeMock]()

	var sets [2][]*fakeMock
	for i := range sets {
		for _, id := range _storeIDs {
			sets[i] = append(sets[i], newFakeMockWith(id, 0))
		}
	}

	require.NoError(t, store.Store(ctx, sets[0]...))

	var (
		wg    sync.WaitGroup
		done  atomic.Bool
		reads atomic.Int64
		torn  atomic.Int64
	)

	for range 4 {
		wg.Go(func() {
			for !done.Load() {
				listed, _ := store.List(ctx)
				found := slices.Collect(listed)

				if !sameMocks(sets[0], found) && !sameMocks(sets[1], found) {
					torn.Add(1)
				}

				reads.Add(1)
			}
		})
	}

	for i := range 2000 {
		require.NoError(t, store.Replace(ctx, sets[i%2], sets[(i+1)%2]...))
	}

	done.Store(true)
	wg.Wait()

	require.Positive(t, reads.Load())
	require.Zero(t, torn.Load(), "reads that found neither set, or a mix")
}

// storeEntry is the model of a stored mock: its order is by priority, then by when it was stored.
type storeEntry struct {
	mock *fakeMock
	seq  int
}

// genStoreMock draws a mock whose ID comes from _storeIDs.
func genStoreMock() *rapid.Generator[*fakeMock] {
	return rapid.Custom(func(t *rapid.T) *fakeMock {
		return newFakeMockWith(rapid.SampledFrom(_storeIDs).Draw(t, "id"), rapid.IntRange(-2, 2).Draw(t, "priority"))
	})
}

func TestMemoryStoreMachine(t *testing.T) {
	// S1: the store behaves like a model: unique IDs, refused batches leave it unchanged, a replacement removes
	// only the very mocks it names, and mocks are ordered by priority, then by when they were stored.
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()
		store := vlatte.NewStore[*fakeMock]()

		var model []storeEntry
		seq := 0

		find := func(id string) int {
			return slices.IndexFunc(model, func(e storeEntry) bool { return e.mock.ID() == id })
		}

		t.Repeat(map[string]func(*rapid.T){
			"store": func(t *rapid.T) {
				batch := rapid.SliceOfN(genStoreMock(), 1, 3).Draw(t, "batch")

				duplicate := false
				seen := make(map[string]bool)

				for _, m := range batch {
					duplicate = duplicate || seen[m.ID()] || find(m.ID()) >= 0
					seen[m.ID()] = true
				}

				err := store.Store(ctx, batch...)
				if duplicate {
					require.ErrorIs(t, err, vlatte.ErrDuplicateID)
					return
				}

				require.NoError(t, err)

				for _, m := range batch {
					seq++
					model = append(model, storeEntry{mock: m, seq: seq})
				}
			},
			"replace": func(t *rapid.T) {
				var old []*fakeMock
				for _, e := range model {
					if rapid.Bool().Draw(t, "replaced") {
						old = append(old, e.mock)
					}
				}

				// A mock that left the store may share its ID with a stored one, which the store must keep.
				if rapid.Bool().Draw(t, "stale") {
					old = append(old, newFakeMockWith(rapid.SampledFrom(_storeIDs).Draw(t, "stale id"), 0))
				}

				batch := rapid.SliceOfN(genStoreMock(), 0, 3).Draw(t, "batch")

				taken := make(map[string]bool)
				for _, e := range model {
					if !slices.Contains(old, e.mock) {
						taken[e.mock.ID()] = true
					}
				}

				duplicate := false
				for _, m := range batch {
					duplicate = duplicate || taken[m.ID()]
					taken[m.ID()] = true
				}

				err := store.Replace(ctx, old, batch...)
				if duplicate {
					require.ErrorIs(t, err, vlatte.ErrDuplicateID)
					return
				}

				require.NoError(t, err)

				model = slices.DeleteFunc(model, func(e storeEntry) bool { return slices.Contains(old, e.mock) })
				for _, m := range batch {
					seq++
					model = append(model, storeEntry{mock: m, seq: seq})
				}
			},
			"delete": func(t *rapid.T) {
				gone := rapid.SliceOfN(rapid.SampledFrom(_storeIDs), 1, 3).Draw(t, "ids")
				require.NoError(t, store.Delete(ctx, gone...))

				model = slices.DeleteFunc(model, func(e storeEntry) bool { return slices.Contains(gone, e.mock.ID()) })
			},
			"clear": func(t *rapid.T) {
				require.NoError(t, store.Clear(ctx))
				model = nil
			},
			"toggle": func(t *rapid.T) {
				if len(model) == 0 {
					t.Skip("nothing stored")
				}

				e := rapid.SampledFrom(model).Draw(t, "mock")
				if e.mock.Enabled() {
					e.mock.Disable()
				} else {
					e.mock.Enable()
				}
			},
			"pick": func(t *rapid.T) {
				id := rapid.SampledFrom(_storeIDs).Draw(t, "id")
				got, err := store.Pick(ctx, id)

				if i := find(id); i >= 0 {
					require.NoError(t, err)
					require.Same(t, model[i].mock, got)
				} else {
					require.ErrorIs(t, err, vlatte.ErrNotFound)
				}
			},
			"": func(t *rapid.T) {
				want := slices.Clone(model)
				slices.SortStableFunc(want, func(a, b storeEntry) int {
					return cmp.Or(cmp.Compare(a.mock.Priority(), b.mock.Priority()), cmp.Compare(a.seq, b.seq))
				})

				wantAll := make([]string, 0, len(want))
				var wantEnabled []string

				for _, e := range want {
					wantAll = append(wantAll, e.mock.ID())
					if e.mock.Enabled() {
						wantEnabled = append(wantEnabled, e.mock.ID())
					}
				}

				all, err := store.PickAll(ctx)
				require.NoError(t, err)
				require.Equal(t, nilIfEmpty(wantAll), nilIfEmpty(ids(all)))

				listed, err := store.List(ctx)
				require.NoError(t, err)
				require.Equal(t, wantEnabled, nilIfEmpty(ids(slices.Collect(listed))))
			},
		})
	})
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return s
}

// fakeBuilder builds a given mock, or fails.
type fakeBuilder struct {
	mock *fakeMock
	err  error
}

func (b fakeBuilder) Build(struct{}) (*fakeMock, error) {
	return b.mock, b.err
}

func TestScopesAdd_StoresNothingWhenABuilderFails(t *testing.T) {
	// A half-added set of mocks would serve without a scope that could assert or clean them.
	store := vlatte.NewStore[*fakeMock]()
	app := vlatte.NewBase[*fakeMock](struct{}{}, store)

	scope, err := app.Mock(
		fakeBuilder{mock: newFakeMockWith("ok", 0)},
		fakeBuilder{err: errors.New("bad file")},
		fakeBuilder{mock: newFakeMockWith("ok2", 0)},
		fakeBuilder{err: errors.New("worse file")},
	)

	require.Nil(t, scope)
	require.EqualError(t, err, "server: mock 1: bad file\nserver: mock 3: worse file")

	all, _ := store.PickAll(t.Context())
	require.Empty(t, all, "no mock was stored")
	require.Empty(t, app.ListPending(), "no scope was added")
}

func TestPropScopesAdd_AllOrNothing(t *testing.T) {
	// S2: an add stores every mock in one scope, or, when any builder fails or an ID is taken, nothing at all,
	// and its error names every failing builder.
	rapid.Check(t, func(t *rapid.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store)

		stored := rapid.SliceOfNDistinct(rapid.SampledFrom(_storeIDs), 0, 3, func(id string) string { return id }).Draw(t, "stored")
		for _, id := range stored {
			_, err := app.Mock(fakeBuilder{mock: newFakeMockWith(id, 0)})
			require.NoError(t, err)
		}

		before, _ := store.PickAll(context.Background())

		builders := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) fakeBuilder {
			if rapid.IntRange(0, 3).Draw(t, "fails") == 0 {
				return fakeBuilder{err: errors.New("bad")}
			}

			return fakeBuilder{mock: newFakeMockWith(rapid.SampledFrom(_storeIDs).Draw(t, "id"), rapid.IntRange(-1, 1).Draw(t, "priority"))}
		}), 0, 5).Draw(t, "builders")

		var failing []string
		taken := slices.Clone(stored)
		duplicate := false

		args := make([]vlatte.Builder[*fakeMock, struct{}], 0, len(builders))
		for i, b := range builders {
			args = append(args, b)

			if b.err != nil {
				failing = append(failing, "server: mock "+strconv.Itoa(i)+": bad")
				continue
			}

			duplicate = duplicate || slices.Contains(taken, b.mock.ID())
			taken = append(taken, b.mock.ID())
		}

		scope, err := app.Mock(args...)
		after, _ := store.PickAll(context.Background())

		switch {
		case len(failing) > 0:
			require.EqualError(t, err, strings.Join(failing, "\n"))
			require.Nil(t, scope)
			require.Equal(t, before, after)
		case duplicate:
			require.ErrorIs(t, err, vlatte.ErrDuplicateID)
			require.Nil(t, scope)
			require.Equal(t, before, after)
		default:
			require.NoError(t, err)

			added := make([]string, 0, len(builders))
			for _, b := range builders {
				added = append(added, b.mock.ID())
			}

			require.ElementsMatch(t, added, scope.IDs())
			require.Len(t, after, len(before)+len(builders))
		}
	})
}
