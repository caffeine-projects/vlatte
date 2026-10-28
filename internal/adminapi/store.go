package adminapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"sync"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Store[vlatte.Mock] = (*Store[vlatte.Mock])(nil)

// Store is the store the admin API changes mocks through, when the mock server keeps them in a persistent store too,
// such as files or a database. It serves from the server's store, which holds every mock and keeps their state, such
// as their hits, and writes each change through to the persistent store at once, so the mocks the admin API adds
// outlive the server. Reload picks up what the persistent store holds, such as changes made to it directly.
//
// The persistent store tells mocks apart by ID: it keeps their descriptions, not the mocks themselves. The mocks the
// server has from elsewhere, such as mock files and code, never reach it: the server stores them in its store
// directly.
type Store[M vlatte.Mock] struct {
	serving vlatte.Store[M]
	durable vlatte.Store[M]
	logger  *zerolog.Logger

	mu    sync.Mutex     // orders the writes, so both stores change in the same order
	owned map[any]string // the mocks it put in serving, those of the persistent store, and their ids
}

// NewStore returns the store of the admin API of a mock server whose store is serving, and whose persistent store is
// durable. logger tells which mocks of durable give way to mocks the server has from elsewhere; nil logs nothing.
func NewStore[M vlatte.Mock](serving, durable vlatte.Store[M], logger *zerolog.Logger) *Store[M] {
	if logger == nil {
		logger = new(zerolog.Nop())
	}

	return &Store[M]{serving: serving, durable: durable, logger: logger, owned: make(map[any]string)}
}

// List returns the enabled mocks the server serves.
func (s *Store[M]) List(ctx context.Context) (iter.Seq[M], error) {
	return s.serving.List(ctx)
}

// Pick returns the mock of id the server serves.
func (s *Store[M]) Pick(ctx context.Context, id string) (M, error) {
	return s.serving.Pick(ctx, id)
}

// PickAll returns every mock the server serves.
func (s *Store[M]) PickAll(ctx context.Context) ([]M, error) {
	return s.serving.PickAll(ctx)
}

// Store stores mocks in the persistent store, then serves them. When the server's store refuses them, such as for an
// ID a mock file's mock holds, the persistent store doesn't keep them either.
func (s *Store[M]) Store(ctx context.Context, mocks ...M) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.durable.Store(ctx, mocks...); err != nil {
		return err
	}

	if err := s.serving.Store(ctx, mocks...); err != nil {
		return errors.Join(err, s.durable.Delete(ctx, ids(mocks)...))
	}

	s.own(mocks)

	return nil
}

// Replace replaces old with mocks in the persistent store, then in the server's. A mock of old that mocks holds again
// is written again, as an edit of its state, such as its enabled state, is persisted, and keeps its place. A mock of
// old the server no longer serves, such as one a reload replaced since it was picked, is left out, as the server's
// store leaves it out: the mock that took its id since, and its description, stay. When the server's store refuses
// mocks, the persistent store gets back the mocks of old it held.
func (s *Store[M]) Replace(ctx context.Context, old []M, mocks ...M) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The persistent store tells mocks apart by id: it gets only the very mocks the server serves.
	served, err := s.serving.PickAll(ctx)
	if err != nil {
		return err
	}

	present := make(map[any]struct{}, len(served))
	for _, mock := range served {
		present[mock] = struct{}{}
	}

	old = slices.DeleteFunc(slices.Clone(old), func(mock M) bool {
		_, ok := present[mock]
		return !ok
	})

	if err := s.durable.Replace(ctx, old, mocks...); err != nil {
		return err
	}

	if err := s.serving.Replace(ctx, old, mocks...); err != nil {
		var held []M
		for _, mock := range old {
			if _, ok := s.owned[mock]; ok {
				held = append(held, mock)
			}
		}

		return errors.Join(err, s.durable.Replace(ctx, mocks, held...))
	}

	s.disown(old)
	s.own(mocks)

	return nil
}

// Delete deletes the mocks of ids from the persistent store, then from the server's.
func (s *Store[M]) Delete(ctx context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.durable.Delete(ctx, ids...); err != nil {
		return err
	}

	for mock, id := range s.owned {
		if slices.Contains(ids, id) {
			delete(s.owned, mock)
		}
	}

	return s.serving.Delete(ctx, ids...)
}

// Clear deletes every mock from the persistent store, then from the server's, those of mock files and code too.
func (s *Store[M]) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.durable.Clear(ctx); err != nil {
		return err
	}

	clear(s.owned)

	return s.serving.Clear(ctx)
}

// Health returns the errors of the persistent store and of the server's.
func (s *Store[M]) Health(ctx context.Context) error {
	return errors.Join(s.durable.Health(ctx), s.serving.Health(ctx))
}

// Reload serves what the persistent store holds now, in place of the mocks it served from it: the changes made to it
// directly take effect. load, when not nil, loads the server's mock files, with swap: it swaps the mocks of the
// persistent store in the same step as those of the files, so a request finds the old ones or the new ones, never a
// mix. A load that doesn't call swap, such as one with no mock files to load, leaves Reload to swap them on its own. A
// mock whose ID a mock the server has from elsewhere holds, such as a mock file's, gives way to it, as mocks the admin
// API adds do. Reload is all or nothing: when the persistent store can't be read, load doesn't run, and when load
// fails, the server serves what it served before.
func (s *Store[M]) Reload(ctx context.Context, load func(swap vlatte.Swap[M]) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fresh, err := s.durable.PickAll(ctx)
	if err != nil {
		return fmt.Errorf("server: admin store: %w", err)
	}

	var kept []M

	swapped := false
	swap := func(built, gone []M) ([]M, []M, error) {
		swapped = true

		drop, add, err := s.take(ctx, fresh, built, gone)
		kept = add

		return drop, add, err
	}

	if load != nil {
		err = load(swap)
	}

	if err == nil && !swapped {
		var drop []M
		if drop, kept, err = s.take(ctx, fresh, nil, nil); err == nil {
			err = s.serving.Replace(ctx, drop, kept...)
		}
	}

	if err != nil {
		closeMocks(fresh)
		return err
	}

	served := make(map[any]struct{}, len(kept))
	for _, mock := range kept {
		served[mock] = struct{}{}
	}

	for _, mock := range fresh {
		if _, ok := served[mock]; !ok {
			s.logger.Warn().Str("mock", mock.ID()).Msg("admin: the persistent store's mock gives way to a mock of its id")
			closeMocks([]M{mock})
		}
	}

	clear(s.owned)
	s.own(kept)

	return nil
}

// take returns the mocks of fresh to serve, those whose IDs no other mock the server keeps holds, and the mocks it
// served from the persistent store to drop. built are the mocks the server adds in the same step, and gone those it
// removes.
func (s *Store[M]) take(ctx context.Context, fresh, built, gone []M) (drop, kept []M, err error) {
	served, err := s.serving.PickAll(ctx)
	if err != nil {
		return nil, nil, err
	}

	leaving := make(map[any]struct{}, len(gone))
	for _, mock := range gone {
		leaving[mock] = struct{}{}
	}

	taken := make(map[string]struct{}, len(served)+len(built))
	for _, mock := range built {
		taken[mock.ID()] = struct{}{}
	}

	for _, mock := range served {
		_, left := leaving[mock]
		_, owned := s.owned[mock]

		switch {
		case left:
		case owned:
			drop = append(drop, mock)
		default:
			taken[mock.ID()] = struct{}{}
		}
	}

	for _, mock := range fresh {
		if _, ok := taken[mock.ID()]; !ok {
			kept = append(kept, mock)
		}
	}

	return drop, kept, nil
}

func (s *Store[M]) own(mocks []M) {
	for _, mock := range mocks {
		s.owned[mock] = mock.ID()
	}
}

func (s *Store[M]) disown(mocks []M) {
	for _, mock := range mocks {
		delete(s.owned, mock)
	}
}

func ids[M vlatte.Mock](mocks []M) []string {
	out := make([]string, 0, len(mocks))
	for _, mock := range mocks {
		out = append(out, mock.ID())
	}

	return out
}

// closeMocks releases what mocks that never reached the server's store hold, such as the HTTP client of a proxy mock.
func closeMocks[M vlatte.Mock](mocks []M) {
	for _, mock := range mocks {
		if c, ok := any(mock).(io.Closer); ok {
			_ = c.Close()
		}
	}
}
