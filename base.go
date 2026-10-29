package vlatte

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Base is what every mock server shares: it adds mocks to its store in scopes, and asserts on them. A protocol's
// server embeds it.
type Base[TM Mock, A any] struct {
	scopes *Scopes[TM, A]
	mu     sync.RWMutex
}

// NewBase creates a [Base] that builds mocks for the server app and stores them in repo.
func NewBase[TM Mock, A any](app A, repo Store[TM]) *Base[TM, A] {
	return &Base[TM, A]{scopes: &Scopes[TM, A]{server: app, store: repo}}
}

// Mock adds one or multiple request mocks. A mock stored outside every scope, such as one the admin API added, gives
// way to a new mock with its ID.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
func (app *Base[TM, A]) Mock(builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Add(builders...)
}

// MustMock adds one or multiple request mocks.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
// It panics if any error occurs.
func (app *Base[TM, A]) MustMock(builders ...Builder[TM, A]) *Scope[TM] {
	scope, err := app.Mock(builders...)
	if err != nil {
		panic(err)
	}

	return scope
}

// Remock builds builders and swaps their mocks for those of old in one step, so a request finds the mocks of old or
// the new ones, never neither, and returns their scope, which takes the place of old. With a nil old, it adds the
// mocks, as Mock does. A mock stored outside every scope, such as one the admin API added, gives way to a new mock with
// its ID. It changes nothing when a builder fails, when the store refuses the mocks, for example over an ID in use in
// another scope, or when old isn't tracked: replaced or unmocked already, or another server's.
func (app *Base[TM, A]) Remock(ctx context.Context, old *Scope[TM], builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Replace(ctx, old, builders...)
}

// Swap gives [Base.RemockWith] more mocks to swap in the same step. built are the mocks of the builders, and gone the
// stored mocks they take the place of: those of the old scope, and those outside every scope whose IDs built take.
// Swap returns the stored mocks to remove too, and the mocks to add too. The new scope doesn't hold those: they stay
// the caller's, to close when RemockWith fails.
type Swap[TM Mock] func(built, gone []TM) (drop, add []TM, err error)

// RemockWith does what Remock does, and swaps the mocks swap returns in the same step, so a request finds the mocks of
// old and those swap drops, or the new ones and those it adds, never a mix. It changes nothing when swap fails. A nil
// swap swaps nothing more.
func (app *Base[TM, A]) RemockWith(ctx context.Context, old *Scope[TM], swap Swap[TM], builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.replace(ctx, old, swap, builders...)
}

// Unmock deletes the mocks of scope and stops tracking it, so Hits, the mock lists and the assertions leave it out.
func (app *Base[TM, A]) Unmock(ctx context.Context, scope *Scope[TM]) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Remove(ctx, scope)
}

// Hits returns the total matched request hits.
func (app *Base[TM, A]) Hits() int64 {
	hits := int64(0)

	for _, s := range app.scopes.All() {
		hits += s.Hits()
	}

	return hits
}

// Enable enables all mocks.
func (app *Base[TM, A]) Enable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scope := range app.scopes.All() {
		scope.Enable()
	}
}

// Disable disables all mocks.
func (app *Base[TM, A]) Disable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scope := range app.scopes.All() {
		scope.Disable()
	}
}

// ListPending returns the mocks of every scope that were not called. It leaves out mocks stored outside every scope,
// such as those the admin API added.
func (app *Base[TM, A]) ListPending() []TM {
	scopes := app.scopes.All()
	ret := make([]TM, 0, len(scopes)/2)
	for _, scope := range scopes {
		ret = append(ret, scope.ListPending()...)
	}

	return ret
}

// ListCalled returns the mocks of every scope that were called at least once. It leaves out mocks stored outside
// every scope, such as those the admin API added.
func (app *Base[TM, A]) ListCalled() []TM {
	scopes := app.scopes.All()
	ret := make([]TM, 0, len(scopes)/2)
	for _, scope := range scopes {
		ret = append(ret, scope.ListCalled()...)
	}

	return ret
}

// Clean removes all scoped mocks.
func (app *Base[TM, A]) Clean(ctx context.Context) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, scope := range app.scopes.All() {
		err := scope.Clear(ctx)
		if err != nil {
			return fmt.Errorf("server: cleaning the mocks of scope %d: %w", i, err)
		}
	}

	return nil
}

// --
// Assertions
// --

// AssertCalled asserts that all mocks associated with this instance were called at least once.
func (app *Base[TM, A]) AssertCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, scope := range app.scopes.All() {
		if scope.Pending() {
			fmt.Fprintf(&buf, "   Scope %d\n", i)
			pending := scope.ListPending()
			size += len(pending)

			for _, p := range pending {
				buf.WriteString("    Mock [")
				buf.WriteString(p.ID())
				buf.WriteString("] ")
				buf.WriteString(p.Name())
				buf.WriteString("\n")
			}

			result = false
		}
	}

	if !result {
		t.Errorf("\nThere are still %d mock(s) that were not called.\n  Pending:\n%s",
			size,
			buf.String(),
		)
	}

	return result
}

// AssertNotCalled asserts that none of the mocks associated with this instance were called.
func (app *Base[TM, A]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, scope := range app.scopes.All() {
		called := scope.ListCalled()
		if len(called) == 0 {
			continue
		}

		fmt.Fprintf(&buf, "   Scope %d\n", i)
		size += len(called)

		for _, p := range called {
			buf.WriteString("    Mock [")
			buf.WriteString(p.ID())
			buf.WriteString("] ")
			buf.WriteString(p.Name())
			buf.WriteString("\n")
		}

		result = false
	}

	if !result {
		t.Errorf(
			"\n%d Mock(s) were called at least once when none should be.\n  Called:\n%s",
			size,
			buf.String(),
		)
	}

	return result
}

// AssertNumberOfCalls asserts that the sum of matched request hits
// is equal to the given expected value.
func (app *Base[TM, A]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := app.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hit(s).\n Got %d", expected, hits)

	return false
}
