package vlatte_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

// modelScope is a scope the server tracks, with the mocks it still holds.
type modelScope struct {
	scope *vlatte.Scope[*fakeMock]
	mocks []*fakeMock
}

func TestScopesMachine(t *testing.T) {
	// S3: a scope only removes the mocks it holds, while the store still holds them, and the server's assertions
	// and hits describe exactly the mocks of the scopes it tracks, whatever mix of adds, remocks, hits, deletes,
	// clears, unmocks, cleans and evictions happens. An eviction removes a mock behind its scope's back and may give
	// its ID to a new mock, which no other scope may then delete. An add or a remock happens in full or not at all.
	ctx := t.Context()

	rapid.Check(t, func(t *rapid.T) {
		store := vlatte.NewStore[*fakeMock]()
		app := vlatte.NewBase[*fakeMock](struct{}{}, store, nil)

		var (
			scopes []*modelScope
			stored []*fakeMock
		)

		tracked := func() []*fakeMock {
			var all []*fakeMock
			for _, s := range scopes {
				all = append(all, s.mocks...)
			}

			return all
		}

		pick := func(t *rapid.T) *modelScope {
			if len(scopes) == 0 {
				t.Skip("no scope")
			}

			return rapid.SampledFrom(scopes).Draw(t, "scope")
		}

		// release takes the mocks of s that ids name out of s and, when the store still holds those very mocks, out of the store.
		release := func(s *modelScope, ids []string) {
			s.mocks = slices.DeleteFunc(s.mocks, func(m *fakeMock) bool {
				if !slices.Contains(ids, m.ID()) {
					return false
				}

				stored = slices.DeleteFunc(stored, func(x *fakeMock) bool { return x == m })

				return true
			})
		}

		own := func(s *modelScope) []string {
			ids := make([]string, 0, len(s.mocks))
			for _, m := range s.mocks {
				ids = append(ids, m.ID())
			}

			return ids
		}

		builders := func(t *rapid.T) []fakeBuilder {
			return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) fakeBuilder {
				if rapid.IntRange(0, 5).Draw(t, "fails") == 0 {
					return fakeBuilder{err: errors.New("bad")}
				}

				return fakeBuilder{mock: newFakeMockWith(rapid.SampledFrom(_storeIDs).Draw(t, "id"), 0)}
			}), 0, 3).Draw(t, "builders")
		}

		// build gives the builders as arguments, and whether they add their mocks: none fails,
		// and no ID repeats or is taken.
		build := func(builders []fakeBuilder, taken map[string]bool) ([]vlatte.Builder[*fakeMock, struct{}], bool) {
			ok := true
			args := make([]vlatte.Builder[*fakeMock, struct{}], 0, len(builders))

			for _, b := range builders {
				args = append(args, b)

				if b.err != nil {
					ok = false
					continue
				}

				ok = ok && !taken[b.mock.ID()]
				taken[b.mock.ID()] = true
			}

			return args, ok
		}

		// added models the scope of builders that added their mocks.
		added := func(scope *vlatte.Scope[*fakeMock], builders []fakeBuilder) *modelScope {
			s := &modelScope{scope: scope}
			for _, b := range builders {
				s.mocks = append(s.mocks, b.mock)
				stored = append(stored, b.mock)
			}

			return s
		}

		t.Repeat(map[string]func(*rapid.T){
			"add": func(t *rapid.T) {
				drawn := builders(t)

				taken := make(map[string]bool, len(stored)+len(drawn))
				for _, m := range stored {
					taken[m.ID()] = true
				}

				args, ok := build(drawn, taken)

				scope, err := app.Mock(args...)
				if !ok {
					require.Error(t, err)
					require.Nil(t, scope)

					return
				}

				require.NoError(t, err)

				scopes = append(scopes, added(scope, drawn))
			},
			"remock": func(t *rapid.T) {
				s := pick(t)
				drawn := builders(t)

				// The mocks of s that the store still holds give their IDs up.
				taken := make(map[string]bool, len(stored)+len(drawn))
				for _, m := range stored {
					if !slices.Contains(s.mocks, m) {
						taken[m.ID()] = true
					}
				}

				args, ok := build(drawn, taken)

				scope, err := app.Remock(ctx, s.scope, args...)
				if !ok {
					require.Error(t, err)
					require.Nil(t, scope)

					return
				}

				require.NoError(t, err)
				release(s, own(s))
				require.Empty(t, s.scope.Mocks())

				scopes[slices.Index(scopes, s)] = added(scope, drawn)
			},
			"hit": func(t *rapid.T) {
				all := tracked()
				if len(all) == 0 {
					t.Skip("no mock")
				}

				rapid.SampledFrom(all).Draw(t, "mock").hit()
			},
			"delete": func(t *rapid.T) {
				s := pick(t)
				ids := rapid.SliceOfN(rapid.SampledFrom(_storeIDs), 0, 3).Draw(t, "ids")

				require.NoError(t, s.scope.Delete(ctx, ids...))
				release(s, ids)
			},
			"clear": func(t *rapid.T) {
				s := pick(t)

				require.NoError(t, s.scope.Clear(ctx))
				release(s, own(s))
			},
			"unmock": func(t *rapid.T) {
				s := pick(t)

				require.NoError(t, app.Unmock(ctx, s.scope))
				release(s, own(s))

				scopes = slices.DeleteFunc(scopes, func(x *modelScope) bool { return x == s })
			},
			"clean": func(t *rapid.T) {
				require.NoError(t, app.Clean(ctx))

				for _, s := range scopes {
					release(s, own(s))
				}
			},
			"evict": func(t *rapid.T) {
				if len(stored) == 0 {
					t.Skip("nothing stored")
				}

				m := rapid.SampledFrom(stored).Draw(t, "mock")

				require.NoError(t, store.Delete(ctx, m.ID()))
				stored = slices.DeleteFunc(stored, func(x *fakeMock) bool { return x == m })

				if rapid.Bool().Draw(t, "reuse the id") {
					newer := newFakeMockWith(m.ID(), 0)

					scope, err := app.Mock(fakeBuilder{mock: newer})
					require.NoError(t, err)

					scopes = append(scopes, &modelScope{scope: scope, mocks: []*fakeMock{newer}})
					stored = append(stored, newer)
				}
			},
			"": func(t *rapid.T) {
				all, err := store.PickAll(ctx)
				require.NoError(t, err)
				require.True(t, sameMocks(stored, all), "the store holds exactly the mocks no scope removed")

				for _, s := range scopes {
					require.True(t, sameMocks(s.mocks, s.scope.Mocks()), "a scope holds exactly its own mocks")
				}

				var hits int64

				anyCalled, allCalled := false, true
				for _, m := range tracked() {
					hits += m.Hits()
					anyCalled = anyCalled || m.Called()
					allCalled = allCalled && m.Called()
				}

				require.Equal(t, hits, app.Hits())
				require.Equal(t, !anyCalled, app.AssertNotCalled(testmock.NewFakeT()))
				require.Equal(t, allCalled, app.AssertCalled(testmock.NewFakeT()))
			},
		})
	})
}
