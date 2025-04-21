package vlatte

import (
	"context"
	"strings"
	"sync"
)

var _ Taste[Bean] = (*Mug[Bean])(nil)

// Mug keeps a reference to a group of mocks.
// With a Mug instance, it is possible to verify if one or a group of mocks were called,
// how many times they were called and so on.
type Mug[M Bean] struct {
	beans map[string]M
	del   map[string]struct{}
	jar   Jar[M]
	mu    sync.RWMutex
}

func Pour[M Bean](store Jar[M], mocks []M) *Mug[M] {
	scoped := &Mug[M]{beans: make(map[string]M, len(mocks)), del: make(map[string]struct{}), jar: store}
	for _, m := range mocks {
		scoped.beans[m.GetID()] = m
	}

	return scoped
}

func (s *Mug[M]) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.beans))
	for _, id := range s.beans {
		ids = append(ids, id.GetID())
	}

	return ids
}

func (s *Mug[M]) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.beans[id]
	return exists
}

// Get returns a Mock with the given ID.
func (s *Mug[M]) Get(id string) M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.beans[id]
}

// GetAll returns all Mock instances kept in this Scope.
func (s *Mug[M]) GetAll() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, len(s.beans))
	i := 0

	for _, m := range s.beans {
		mocks[i] = m
		i++
	}

	return mocks
}

// ListPending returns all Mock instances that were not called at least once.
func (s *Mug[M]) ListPending() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.beans))

	for _, m := range s.beans {
		if !m.Called() {
			mocks = append(mocks, m)
		}
	}

	return mocks
}

// ListCalled returns all Mock instances that were called.
func (s *Mug[M]) ListCalled() []M {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.beans))

	for _, m := range s.beans {
		if m.Called() {
			mocks = append(mocks, m)
		}
	}

	return mocks
}

// Called returns true if all Scope Mock instances were called at least once.
func (s *Mug[M]) Called() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.GetAll() {
		if !m.Called() {
			return false
		}
	}

	return true
}

// Pending returns true when there are one or more Mock instances that were not called at least once.
func (s *Mug[M]) Pending() bool {
	for _, m := range s.GetAll() {
		if !m.Called() {
			return true
		}
	}

	return false
}

// Disable scoped store.
// Disabled Mock will be ignored.
func (s *Mug[M]) Disable() {
	for _, m := range s.GetAll() {
		m.Disable()
	}
}

// Enable all Mock instances kept in this Scope.
func (s *Mug[M]) Enable() {
	for _, m := range s.GetAll() {
		m.Enable()
	}
}

// Delete removes a by ID, as long as it is scoped by this instance.
func (s *Mug[M]) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.beans[id]
	if !ok {
		return false
	}

	delete(s.beans, id)
	s.del[id] = struct{}{}

	return true
}

// Clean all scoped Mock instances.
func (s *Mug[M]) Clean(ctx context.Context) error {
	for id := range s.beans {
		s.Delete(id)
	}

	return s.Sync(ctx)
}

// Hits returns the sum of the Scope store calls.
func (s *Mug[M]) Hits() int64 {
	total := int64(0)
	for _, m := range s.GetAll() {
		total += m.Hits()
	}

	return total
}

func (s *Mug[M]) Sync(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mocks := make([]M, 0, len(s.beans))
	for _, v := range s.beans {
		mocks = append(mocks, v)
	}

	if err := s.jar.Save(ctx, mocks...); err != nil {
		return err
	}

	ids := make([]string, 0, len(s.beans))
	for k := range s.del {
		ids = append(ids, k)
	}

	for _, id := range ids {
		err := s.jar.Del(ctx, id)
		if err != nil {
			return err
		}

		delete(s.del, id)
	}

	return nil
}

// AssertCalled reports an error if there are still pending Mock instances.
func (s *Mug[M]) AssertCalled(t TestingT) bool {
	t.Helper()

	if !s.Pending() {
		return true
	}

	b := strings.Builder{}
	pending := s.ListPending()
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
func (s *Mug[M]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	if s.Pending() {
		return true
	}

	b := strings.Builder{}
	called := s.ListCalled()
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
func (s *Mug[M]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := s.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hits.\nGot %d", expected, hits)

	return false
}
