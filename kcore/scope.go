package kcore

import (
	"context"
	"strings"
	"sync"
)

// Scope keeps a reference to a group of mocks.
// With a Scope instance, it is possible to verify if one or a group of mocks were called,
// how many times they were called and so on.
type Scope[M Mock] struct {
	mocks map[string]M
	del   map[string]struct{}
	store MockRepository[M]
	mu    sync.RWMutex
}

func Scoped[M Mock](store MockRepository[M], mocks []M) *Scope[M] {
	scoped := &Scope[M]{mocks: make(map[string]M, len(mocks)), del: make(map[string]struct{}), store: store}
	for _, m := range mocks {
		scoped.mocks[m.GetID()] = m
	}

	return scoped
}

func (s *Scope[M]) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.mocks))
	for _, id := range s.mocks {
		ids = append(ids, id.GetID())
	}

	return ids
}

func (s *Scope[M]) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.mocks[id]
	return exists
}

// Get returns a Mock with the given ID.
func (s *Scope[M]) Get(id string) M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.mocks[id]
}

// GetAll returns all Mock instances kept in this Scope.
func (s *Scope[M]) GetAll() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, len(s.mocks))
	i := 0

	for _, m := range s.mocks {
		mocks[i] = m
		i++
	}

	return mocks
}

// GetPending returns all Mock instances that were not called at least once.
func (s *Scope[M]) GetPending() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.mocks))

	for _, m := range s.mocks {
		if !m.HasBeenCalled() {
			mocks = append(mocks, m)
		}
	}

	return mocks
}

// GetCalled returns all Mock instances that were called.
func (s *Scope[M]) GetCalled() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.mocks))

	for _, m := range s.mocks {
		if m.HasBeenCalled() {
			mocks = append(mocks, m)
		}
	}

	return mocks
}

// Called returns true if all Scope Mock instances were called at least once.
func (s *Scope[M]) Called() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.GetAll() {
		if !m.HasBeenCalled() {
			return false
		}
	}

	return true
}

// Pending returns true when there are one or more Mock instances that were not called at least once.
func (s *Scope[M]) Pending() bool {
	for _, m := range s.GetAll() {
		if !m.HasBeenCalled() {
			return true
		}
	}

	return false
}

// Disable scoped store.
// Disabled Mock will be ignored.
func (s *Scope[M]) Disable() {
	for _, m := range s.GetAll() {
		m.Disable()
	}
}

// Enable all Mock instances kept in this Scope.
func (s *Scope[M]) Enable() {
	for _, m := range s.GetAll() {
		m.Enable()
	}
}

// Delete removes a by ID, as long as it is scoped by this instance.
func (s *Scope[M]) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.mocks[id]
	if !ok {
		return false
	}

	delete(s.mocks, id)
	s.del[id] = struct{}{}

	return true
}

// Clean all scoped Mock instances.
func (s *Scope[M]) Clean(ctx context.Context) error {
	for id := range s.mocks {
		s.Delete(id)
	}

	return s.Sync(ctx)
}

// Hits returns the sum of the Scope store calls.
func (s *Scope[M]) Hits() int64 {
	total := int64(0)
	for _, m := range s.GetAll() {
		total += m.Hits()
	}

	return total
}

func (s *Scope[M]) Sync(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.mocks))
	for _, v := range s.mocks {
		mocks = append(mocks, v)
	}

	if err := s.store.Save(ctx, mocks...); err != nil {
		return err
	}

	ids := make([]string, 0, len(s.mocks))
	for k := range s.del {
		ids = append(ids, k)
	}

	for _, id := range ids {
		err := s.store.Delete(ctx, id)
		if err != nil {
			return err
		}

		delete(s.del, id)
	}

	return nil
}

// AssertCalled reports an error if there are still pending Mock instances.
func (s *Scope[M]) AssertCalled(t TestingT) bool {
	t.Helper()

	if !s.Pending() {
		return true
	}

	b := strings.Builder{}
	pending := s.GetPending()
	size := len(pending)

	for _, p := range pending {
		b.WriteString("   Mock [")
		b.WriteString(p.GetID())
		b.WriteString("] ")
		b.WriteString(p.GetName())
		b.WriteString("\n")
	}

	t.Errorf("\nThere are still %d mocks that were not called.\nPending:\n%s", size, b.String())

	return false
}

// AssertNotCalled reports an error if any mock was called.
func (s *Scope[M]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	if s.Pending() {
		return true
	}

	b := strings.Builder{}
	called := s.GetCalled()
	size := len(called)

	for _, p := range called {
		b.WriteString("  Mock [")
		b.WriteString(p.GetID())
		b.WriteString("] ")
		b.WriteString(p.GetName())
		b.WriteString("\n")
	}

	t.Errorf("\n%d Mocks were called at least once when none should be.\nCalled:\n%s", size, b.String())

	return false
}

// AssertNumberOfCalls asserts that the sum of matched request hits
// is equal to the given expected value.
func (s *Scope[M]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := s.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hits.\nGot %d", expected, hits)

	return false
}
