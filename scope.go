package vlatte

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

var _ Inspector[Mock] = (*Scope[Mock])(nil)

var errNotTracked = errors.New("server: the scope to replace isn't tracked: it was replaced or unmocked, or is another server's")

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

// Delete removes the mocks of this scope with the given IDs from the store. IDs of other mocks are ignored.
// A mock that left the store some other way stays out, even when its ID now names another mock:
// mocks are told apart by identity, so Mock implementations must be comparable, as pointers are.
func (s *Scope[B]) Delete(ctx context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		e, ok := s.index[id]
		if !ok {
			continue
		}

		stored, err := s.store.Pick(ctx, id)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return err
		case any(stored) == any(e.mock):
			owned = append(owned, id)
		}
	}

	if len(owned) > 0 {
		if err := s.store.Delete(ctx, owned...); err != nil {
			return err
		}
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

	called := s.ListCalled()
	if len(called) == 0 {
		return true
	}

	b := strings.Builder{}
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

// replace swaps the mocks of this scope that the store still holds, and those of drop, for mocks, in one step, and
// empties the scope.
func (s *Scope[B]) replace(ctx context.Context, mocks, drop []B) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Read under the lock already held: Mocks would take the read lock, which isn't reentrant.
	own := make([]B, 0, len(s.index)+len(drop))
	for _, e := range s.index {
		own = append(own, e.mock)
	}

	if err := s.store.Replace(ctx, append(own, drop...), mocks...); err != nil {
		return err
	}

	clear(s.index)

	return nil
}

// Scopes is a collection of Scope instances with coordinated add and retrieval.
type Scopes[B Mock, A any] struct {
	mu     sync.RWMutex
	server A
	store  Store[B]
	list   []*Scope[B]
}

// Add builds every builder, then stores the mocks in one call and returns their scope.
// It stores nothing when a builder fails, and reports every failing builder by index,
// or when the store refuses the mocks, for example over an ID already in use.
func (s *Scopes[B, A]) Add(builders ...Builder[B, A]) (*Scope[B], error) {
	return s.Replace(context.Background(), nil, builders...)
}

// Replace builds every builder, then swaps the mocks for those of old in one step, so a reader finds the mocks of
// old or the new ones, never neither, and returns their scope, which takes the place of old. With a nil old, it adds
// the mocks, as Add does. A mock stored outside every scope, such as one the admin API added, gives way in the same
// step to a new mock with its ID. It changes nothing when old isn't tracked, when a builder fails, reporting every
// failing builder by index, or when the store refuses the mocks, for example over an ID in use in another scope.
// Once replaced, old holds no mocks.
func (s *Scopes[B, A]) Replace(ctx context.Context, old *Scope[B], builders ...Builder[B, A]) (*Scope[B], error) {
	return s.replace(ctx, old, nil, builders...)
}

// replace does what Replace does, and swaps the mocks of swap, when not nil, in the same step: see [Swap].
func (s *Scopes[B, A]) replace(ctx context.Context, old *Scope[B], swap Swap[B], builders ...Builder[B, A]) (*Scope[B], error) {
	if old != nil && !slices.Contains(s.All(), old) {
		return nil, errNotTracked
	}

	built := make([]B, 0, len(builders))

	var errs []error
	for i, b := range builders {
		mock, err := b.Build(s.server)
		if err != nil {
			errs = append(errs, fmt.Errorf("server: mock %d: %w", i, err))
			continue
		}

		built = append(built, mock)
	}

	// The mocks built never reach the store when another fails, or when the store refuses them: nothing else would
	// close them.
	if len(errs) > 0 {
		s.closeBuilt(ctx, built)
		return nil, errors.Join(errs...)
	}

	unscoped, err := s.unscoped(ctx, built)

	drop, mocks := unscoped, built
	if err == nil && swap != nil {
		gone := unscoped
		if old != nil {
			gone = slices.Concat(old.Mocks(), unscoped)
		}

		var more, add []B
		if more, add, err = swap(built, gone); err == nil {
			drop, mocks = slices.Concat(unscoped, more), slices.Concat(built, add)
		}
	}

	switch {
	case err != nil:
	case old != nil:
		err = old.replace(ctx, mocks, drop)
	case len(drop) > 0:
		err = s.store.Replace(ctx, drop, mocks...)
	default:
		err = s.store.Store(ctx, mocks...)
	}

	if err != nil {
		s.closeBuilt(ctx, built)
		return nil, fmt.Errorf("server: %w", err)
	}

	scope := NewScope(built, s.store)

	// In the place of old, so the scopes keep their numbers in reports, and nothing reading them finds neither.
	s.mu.Lock()
	if i := slices.Index(s.list, old); i >= 0 {
		s.list[i] = scope
	} else {
		s.list = append(s.list, scope)
	}
	s.mu.Unlock()

	return scope, nil
}

// Remove deletes the mocks of scope and stops tracking it.
func (s *Scopes[B, A]) Remove(ctx context.Context, scope *Scope[B]) error {
	if err := scope.Clear(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.list = slices.DeleteFunc(s.list, func(e *Scope[B]) bool { return e == scope })
	s.mu.Unlock()

	return nil
}

func (s *Scopes[B, A]) All() []*Scope[B] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]*Scope[B], len(s.list))
	copy(ret, s.list)

	return ret
}

// unscoped returns the stored mocks no scope holds whose IDs mocks take.
func (s *Scopes[B, A]) unscoped(ctx context.Context, mocks []B) ([]B, error) {
	ids := make(map[string]struct{}, len(mocks))
	for _, mock := range mocks {
		ids[mock.ID()] = struct{}{}
	}

	stored, err := s.store.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	taken := slices.DeleteFunc(stored, func(mock B) bool {
		_, ok := ids[mock.ID()]
		return !ok
	})

	if len(taken) == 0 {
		return nil, nil
	}

	scoped := make(map[any]struct{})
	for _, scope := range s.All() {
		for _, mock := range scope.Mocks() {
			scoped[mock] = struct{}{}
		}
	}

	return slices.DeleteFunc(taken, func(mock B) bool {
		_, ok := scoped[mock]
		return ok
	}), nil
}

// closeBuilt closes the mocks a call built and couldn't store. A builder that returns a mock the store holds, which
// Builder forbids, gets it back open: it still serves.
func (s *Scopes[B, A]) closeBuilt(ctx context.Context, built []B) {
	for _, mock := range built {
		if stored, err := s.store.Pick(ctx, mock.ID()); err == nil && any(stored) == any(mock) {
			continue
		}

		closeMocks([]B{mock})
	}
}
