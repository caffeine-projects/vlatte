package vlatte

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type MockApp[TM Bean] interface {
	Hits() int64
	Enable()
	Disable()
	Clean(ctx context.Context) error
}

type Boilerplate[TM Bean, A MockApp[TM]] struct {
	tray *Tray[TM, A]
	mu   sync.RWMutex
}

func NewBaseApp[TM Bean, A MockApp[TM]](app A, repo Jar[TM]) *Boilerplate[TM, A] {
	return &Boilerplate[TM, A]{tray: &Tray[TM, A]{brewer: app, jar: repo}}
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
func (app *Boilerplate[TM, A]) Mock(builders ...Builder[TM, A]) (*Mug[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.tray.Add(builders...)
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
func (app *Boilerplate[TM, A]) MustMock(builders ...Builder[TM, A]) *Mug[TM] {
	mug, err := app.Mock(builders...)
	if err != nil {
		panic(err)
	}

	return mug
}

// Hits returns the total matched request hits.
func (app *Boilerplate[TM, A]) Hits() int64 {
	hits := int64(0)

	for _, s := range app.tray.Mugs() {
		hits += s.Hits()
	}

	return hits
}

// Enable enables all mocks.
func (app *Boilerplate[TM, A]) Enable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scoped := range app.tray.Mugs() {
		scoped.Enable()
	}
}

// Disable disables all mocks.
func (app *Boilerplate[TM, A]) Disable() {
	for _, scoped := range app.tray.Mugs() {
		scoped.Disable()
	}
}

func (app *Boilerplate[TM, A]) ListPending() []TM {
	mugs := app.tray.Mugs()
	ret := make([]TM, 0, len(mugs)/2)
	for _, mug := range mugs {
		ret = append(ret, mug.ListPending()...)
	}

	return ret
}

func (app *Boilerplate[TM, A]) ListCalled() []TM {
	mugs := app.tray.Mugs()
	ret := make([]TM, 0, len(mugs)/2)
	for _, mug := range mugs {
		ret = append(ret, mug.ListCalled()...)
	}

	return ret
}

// Clean removes all scoped mocks.
func (app *Boilerplate[TM, A]) Clean(ctx context.Context) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, s := range app.tray.Mugs() {
		err := s.Clean(ctx)
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
func (app *Boilerplate[TM, A]) AssertCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, s := range app.tray.Mugs() {
		if s.Pending() {
			buf.WriteString(fmt.Sprintf("   Scope %d\n", i))
			pending := s.ListPending()
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
func (app *Boilerplate[TM, A]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, s := range app.tray.Mugs() {
		if !s.Pending() {
			buf.WriteString(fmt.Sprintf("   Scope %d\n", i))
			called := s.ListCalled()
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
func (app *Boilerplate[TM, A]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := app.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hit(s).\n Got %d", expected, hits)

	return false
}
