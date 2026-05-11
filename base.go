package vlatte

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type MockApp[TM Mock] interface {
	Hits() int64
	Enable()
	Disable()
	Clean(ctx context.Context) error
}

type Base[TM Mock, A MockApp[TM]] struct {
	registry *Registry[TM, A]
	mu       sync.RWMutex
}

func NewBase[TM Mock, A MockApp[TM]](app A, repo Store[TM]) *Base[TM, A] {
	return &Base[TM, A]{registry: &Registry[TM, A]{flavor: app, store: repo}}
}

// Mock adds one or multiple request mocks.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
func (app *Base[TM, A]) Mock(builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.registry.Add(builders...)
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

// Hits returns the total matched request hits.
func (app *Base[TM, A]) Hits() int64 {
	hits := int64(0)

	for _, s := range app.registry.Scopes() {
		hits += s.Hits()
	}

	return hits
}

// Enable enables all mocks.
func (app *Base[TM, A]) Enable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scope := range app.registry.Scopes() {
		scope.Enable()
	}
}

// Disable disables all mocks.
func (app *Base[TM, A]) Disable() {
	for _, scope := range app.registry.Scopes() {
		scope.Disable()
	}
}

func (app *Base[TM, A]) ListPending() []TM {
	scopes := app.registry.Scopes()
	ret := make([]TM, 0, len(scopes)/2)
	for _, scope := range scopes {
		ret = append(ret, scope.ListPending()...)
	}

	return ret
}

func (app *Base[TM, A]) ListCalled() []TM {
	scopes := app.registry.Scopes()
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

	for i, scope := range app.registry.Scopes() {
		err := scope.Clear(ctx)
		if err != nil {
			return fmt.Errorf("error cleaning mocks from scope %d. %w", i, err)
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

	for i, scope := range app.registry.Scopes() {
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

	for i, scope := range app.registry.Scopes() {
		if !scope.Pending() {
			fmt.Fprintf(&buf, "   Scope %d\n", i)
			called := scope.ListCalled()
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
