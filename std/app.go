package std

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

var _ MockApp[*M] = (*baseApp)(nil)

// TestingT is based on testing.T and is used for assertions.
// See Assert* methods on the application instance.
type TestingT interface {
	Helper()
	Logf(format string, a ...any)
	Error(a ...any)
	Errorf(format string, a ...any)
	Cleanup(func())
}

// Extension describes a component that can be registered within the mock server and used lately.
// Only one instance of a specific Extension should be registered, but, depending on the component,
// it could be used many times.
type Extension interface {
	UniqueName() string
}

type Assertions interface {
	AssertCalled(t TestingT) bool
	AssertNotCalled(t TestingT) bool
	AssertNumberOfCalls(t TestingT, expected int) bool
}

type MockApp[TMock Mock] interface {
	Hits() int64
	Enable()
	Disable()
	Clean() error
}

type MockBag[TMock Mock, A MockApp[TMock]] struct {
	mu     sync.RWMutex
	app    A
	store  Store[TMock]
	scopes []*Scope[TMock]
}

func (a *MockBag[TMock, A]) Add(builders ...Builder[TMock, A]) (*Scope[TMock], error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	added := make([]TMock, len(builders))

	for i, b := range builders {
		mock, err := b.Build(a.app)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		mock.Prepare()

		if err := a.store.Save(context.Background(), mock); err != nil {
			return nil, err
		}

		added[i] = mock
	}

	scope := Scoped(a.store, added)
	a.scopes = append(a.scopes, scope)

	return scope, nil
}

func (a *MockBag[TMock, A]) List() []*Scope[TMock] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.scopes
}

type BaseApp[TMock Mock, A MockApp[TMock]] struct {
	mockBag *MockBag[TMock, A]
	mu      sync.RWMutex
}

func NewBaseApp[TMock Mock, A MockApp[TMock]](app A, repo Store[TMock]) *BaseApp[TMock, A] {
	return &BaseApp[TMock, A]{mockBag: &MockBag[TMock, A]{app: app, store: repo}}
}

// Mock adds one or multiple request mocks.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
//
// Usage:
//
//	scoped := m.MustMock(
//		Get(matcher.URLPath("/test")).
//			Header("test", matcher.StrictEqual("hello")).
//			Query("filter", matcher.StrictEqual("all")).
//			Reply(reply.Created().PlainText("hello world")))
//
//	assert.True(txtTemplate, scoped.HasBeenCalled())
func (app *BaseApp[TMock, A]) Mock(builders ...Builder[TMock, A]) (*Scope[TMock], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.mockBag.Add(builders...)
}

// MustMock adds one or multiple request mocks.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
// It fails immediately if any error occurs.
//
// Usage:
//
//	scoped := m.MustMock(
//		Get(matcher.URLPath("/test")).
//			Header("test", matcher.StrictEqual("hello")).
//			Query("filter", matcher.StrictEqual("all")).
//			Reply(reply.Created().PlainText("hello world")))
//
//	assert.True(txtTemplate, scoped.HasBeenCalled())
func (app *BaseApp[TMock, A]) MustMock(builders ...Builder[TMock, A]) *Scope[TMock] {
	scoped, err := app.Mock(builders...)
	if err != nil {
		panic(err)
	}

	return scoped
}

// Hits returns the total matched request hits.
func (app *BaseApp[TMock, A]) Hits() int64 {
	hits := int64(0)

	for _, s := range app.mockBag.List() {
		hits += s.Hits()
	}

	return hits
}

// Enable enables all mocks.
func (app *BaseApp[TMock, A]) Enable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scoped := range app.mockBag.List() {
		scoped.Enable()
	}
}

// Disable disables all mocks.
func (app *BaseApp[TMock, A]) Disable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scoped := range app.mockBag.List() {
		scoped.Disable()
	}
}

// Clean removes all scoped mocks.
func (app *BaseApp[TMock, A]) Clean() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, s := range app.mockBag.List() {
		err := s.Clean(context.Background())
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
func (app *BaseApp[TMock, A]) AssertCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, s := range app.mockBag.List() {
		if s.Pending() {
			buf.WriteString(fmt.Sprintf("   Scope %d\n", i))
			pending := s.GetPending()
			size += len(pending)

			for _, p := range pending {
				buf.WriteString("    Mock [")
				buf.WriteString(p.GetID())
				buf.WriteString("] ")
				buf.WriteString(p.GetName())
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

// AssertNotCalled asserts that all mocks associated with this instance were called at least once.
func (app *BaseApp[TMock, A]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, s := range app.mockBag.List() {
		if !s.Pending() {
			buf.WriteString(fmt.Sprintf("   Scope %d\n", i))
			called := s.GetCalled()
			size += len(called)

			for _, p := range called {
				buf.WriteString("    Mock [")
				buf.WriteString(p.GetID())
				buf.WriteString("] ")
				buf.WriteString(p.GetName())
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
func (app *BaseApp[TMock, A]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := app.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hit(s).\n Got %d", expected, hits)

	return false
}

type baseApp struct {
	BaseApp[*M, *baseApp]
}

func (a *baseApp) Hits() int64  { return 0 }
func (a *baseApp) Enable()      {}
func (a *baseApp) Disable()     {}
func (a *baseApp) Clean() error { return nil }
