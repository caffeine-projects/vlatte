package vlatte

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

var _ Inspector[Mock] = (*Scope[Mock])(nil)

type entry[B Mock] struct {
	mock B
}

func indexByID[B Mock](mocks []B) map[string]entry[B] {
	m := make(map[string]entry[B], len(mocks))
	for _, b := range mocks {
		m[b.ID()] = entry[B]{mock: b}
	}

	return m
}

// NewScope creates a Scope that holds references to the given mocks and store.
// With a Scope instance, it is possible to verify if one or a group of mocks were called,
// how many times they were called and so on.
func NewScope[B Mock](mocks []B, store Store[B]) *Scope[B] {
	return &Scope[B]{index: indexByID(mocks), store: store}
}

// Scope keeps a reference to a group of mocks.
type Scope[B Mock] struct {
	index map[string]entry[B]
	store Store[B]
	mu    sync.RWMutex
}

// Mock returns the Mock with the given ID, or the zero value if not found.
func (s *Scope[B]) Mock(id string) B {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b := s.index[id]
	return b.mock
}

// Mocks returns all Mock instances kept in this Scope.
func (s *Scope[B]) Mocks() []B {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]B, len(s.index))
	i := 0

	for _, m := range s.index {
		mocks[i] = m.mock
		i++
	}

	return mocks
}

func (s *Scope[B]) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.index))
	for _, id := range s.index {
		ids = append(ids, id.mock.ID())
	}

	return ids
}

func (s *Scope[B]) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.index[id]
	return exists
}

// ListPending returns all Mock instances that were not called at least once.
func (s *Scope[B]) ListPending() []B {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]B, 0, len(s.index))

	for _, m := range s.index {
		if !m.mock.Called() {
			mocks = append(mocks, m.mock)
		}
	}

	return mocks
}

// ListCalled returns all Mock instances that were called.
func (s *Scope[B]) ListCalled() []B {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]B, 0, len(s.index))

	for _, m := range s.index {
		if m.mock.Called() {
			mocks = append(mocks, m.mock)
		}
	}

	return mocks
}

// Called returns true if all Scope Mock instances were called at least once.
func (s *Scope[B]) Called() bool {
	for _, m := range s.Mocks() {
		if !m.Called() {
			return false
		}
	}

	return true
}

// Pending returns true when there are one or more Mock instances that were not called at least once.
func (s *Scope[B]) Pending() bool {
	for _, m := range s.Mocks() {
		if !m.Called() {
			return true
		}
	}

	return false
}

// Disable disables all Mock instances kept in this Scope.
// Disabled Mocks will be ignored during matching.
func (s *Scope[B]) Disable() {
	for _, m := range s.Mocks() {
		m.Disable()
	}
}

// Enable enables all Mock instances kept in this Scope.
func (s *Scope[B]) Enable() {
	for _, m := range s.Mocks() {
		m.Enable()
	}
}

// Hits returns the sum of all matched request hits across the Scope.
func (s *Scope[B]) Hits() int64 {
	total := int64(0)
	for _, m := range s.Mocks() {
		total += m.Hits()
	}

	return total
}

// Delete removes mocks by ID, as long as they are scoped by this instance.
func (s *Scope[B]) Delete(ctx context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.store.Delete(ctx, ids...); err != nil {
		return err
	}

	for _, id := range ids {
		delete(s.index, id)
	}

	return nil
}

// Clear removes all scoped Mock instances.
func (s *Scope[B]) Clear(ctx context.Context) error {
	return s.Delete(ctx, s.IDs()...)
}

func (s *Scope[B]) Sync(ctx context.Context) error {
	s.mu.RLock()
	mocks := s.Mocks()
	b := make([]B, len(mocks))
	copy(b, mocks)
	s.mu.RUnlock()

	if err := s.store.Store(ctx, b...); err != nil {
		return err
	}

	return nil
}

// AssertCalled reports an error if there are still pending Mock instances.
func (s *Scope[B]) AssertCalled(t TestingT) bool {
	t.Helper()

	if !s.Pending() {
		return true
	}

	b := strings.Builder{}
	pending := s.ListPending()
	size := len(pending)

	for _, p := range pending {
		b.WriteString("   Mock [")
		b.WriteString(p.ID())
		b.WriteString("] ")
		b.WriteString(p.Name())
		b.WriteString("\n")
	}

	t.Errorf("\nThere are still %d mocks that were not called.\nPending:\n%s", size, b.String())

	return false
}

// AssertNotCalled reports an error if any mock was called.
func (s *Scope[B]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	if s.Pending() {
		return true
	}

	b := strings.Builder{}
	called := s.ListCalled()
	size := len(called)

	for _, p := range called {
		b.WriteString("  Mock [")
		b.WriteString(p.ID())
		b.WriteString("] ")
		b.WriteString(p.Name())
		b.WriteString("\n")
	}

	t.Errorf("\n%d Mocks were called at least once when none should be.\nCalled:\n%s", size, b.String())

	return false
}

// AssertNumberOfCalls asserts that the sum of matched request hits
// is equal to the given expected value.
func (s *Scope[B]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := s.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hits.\nGot %d", expected, hits)

	return false
}

// Scopes is a collection of Scope instances with coordinated add and retrieval.
type Scopes[B Mock, A any] struct {
	mu     sync.RWMutex
	flavor A
	store  Store[B]
	list   []*Scope[B]
}

func (s *Scopes[B, A]) Add(builders ...Builder[B, A]) (*Scope[B], error) {
	ground := make([]B, len(builders))

	for i, b := range builders {
		built, err := b.Build(s.flavor)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		if err := s.store.Store(context.Background(), built); err != nil {
			return nil, err
		}

		ground[i] = built
	}

	scope := NewScope(ground, s.store)

	s.mu.Lock()
	s.list = append(s.list, scope)
	s.mu.Unlock()

	return scope, nil
}

func (s *Scopes[B, A]) All() []*Scope[B] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]*Scope[B], len(s.list))
	copy(ret, s.list)

	return ret
}
