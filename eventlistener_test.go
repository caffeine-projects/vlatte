package vlatte_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// completed is the event a listener gets in these tests.
type completed struct {
	mock   vlatte.Mock
	params vlatte.Parameters
}

func (c completed) Kind() vlatte.Kind             { return "test" }
func (c completed) Kit() vlatte.Kit               { return nil }
func (c completed) Parameters() vlatte.Parameters { return c.params }
func (c completed) Mock() vlatte.Mock             { return c.mock }

// argsOf returns the argsFn of a runner for events of mock.
func argsOf(mock vlatte.Mock) func(context.Context, vlatte.EventConfig) vlatte.OnComplete {
	return func(_ context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
		return completed{mock: mock, params: cfg.Parameters}
	}
}

// reported collects what a runner reports, from the goroutines of its events.
type reported struct {
	mu   sync.Mutex
	errs []string
}

func (r *reported) add(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.errs = append(r.errs, err.Error())
}

func (r *reported) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Sorted(slices.Values(r.errs))
}

func register(t *testing.T, listeners *vlatte.Listener, name string, fn func(context.Context, vlatte.OnComplete) error) {
	t.Helper()
	require.NoError(t, listeners.OnComplete.Register(name, vlatte.EventFunc[vlatte.OnComplete](fn)))
}

func mockWith(names ...string) *fakeMock {
	m := newFakeMock()
	for _, name := range names {
		m.events.OnComplete = append(m.events.OnComplete, vlatte.EventConfig{Name: name})
	}

	return m
}

func TestEventRunner_EachEventRunsOnItsOwn(t *testing.T) {
	// #15: events ran one after the other, inside the handler, and the first error skipped the rest: a failing global
	// webhook stopped the mock's own. Each runs in a goroutine of its own now: a blocked event holds none of the others,
	// and a failing one skips none.
	synctest.Test(t, func(t *testing.T) {
		unblock := make(chan struct{})
		var ran sync.Map

		listeners := vlatte.NewListener()
		register(t, listeners, "blocked", func(context.Context, vlatte.OnComplete) error {
			<-unblock
			ran.Store("blocked", true)

			return nil
		})
		register(t, listeners, "failing", func(context.Context, vlatte.OnComplete) error {
			ran.Store("failing", true)
			return errors.New("target down")
		})
		register(t, listeners, "mock's", func(context.Context, vlatte.OnComplete) error {
			ran.Store("mock's", true)
			return nil
		})

		global := &vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "blocked"}, {Name: "failing"}}}
		runner := vlatte.NewEventRunner(listeners, global)
		mock := mockWith("mock's")

		var got reported
		runner.OnComplete(mock, argsOf(mock), got.add)

		synctest.Wait()
		_, failed := ran.Load("failing")
		_, own := ran.Load("mock's")
		_, blocked := ran.Load("blocked")
		require.True(t, failed && own, "the blocked event holds none of the others")
		require.False(t, blocked)
		require.Equal(t, []string{"global on_complete listener failing at index 1: target down"}, got.all())

		close(unblock)
		runner.Wait()

		_, blocked = ran.Load("blocked")
		require.True(t, blocked)
	})
}

func TestEventRunner_ReportsUnregisteredNamesAndPanics(t *testing.T) {
	// A listener's panic was caught by the server's recovery while listeners ran in its handler: in a goroutine, it
	// would end the process. It's reported with its stack now, as an unregistered name is, and the other events run.
	synctest.Test(t, func(t *testing.T) {
		var ran atomic.Bool

		listeners := vlatte.NewListener()
		register(t, listeners, "panics", func(context.Context, vlatte.OnComplete) error { panic("boom") })
		register(t, listeners, "runs", func(context.Context, vlatte.OnComplete) error {
			ran.Store(true)
			return nil
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		mock := mockWith("panics", "nobody", "runs")

		var got reported
		runner.OnComplete(mock, argsOf(mock), got.add)
		runner.Wait()

		errs := got.all()
		require.Len(t, errs, 2)
		require.Equal(t, "on_complete listener nobody at index 1 is not registered", errs[0])
		require.True(t, strings.HasPrefix(errs[1], "on_complete listener panics at index 0: panic: boom\n"), errs[1])
		require.Contains(t, errs[1], "goroutine", "the stack of the panic")
		require.True(t, ran.Load())
	})
}

func TestEventRunner_WaitWaitsForEventsAndHolds(t *testing.T) {
	// A response can reach the client before its handler starts the events, so a request holds the runner until then:
	// Wait mustn't return between the response and the start of its events.
	synctest.Test(t, func(t *testing.T) {
		var ran atomic.Bool

		listeners := vlatte.NewListener()
		register(t, listeners, "hook", func(context.Context, vlatte.OnComplete) error {
			time.Sleep(time.Second)
			ran.Store(true)

			return nil
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		runner.Wait() // nothing counted

		// A mock without events holds nothing.
		runner.Hold(mockWith())
		runner.Wait()

		mock := mockWith("hook")
		release := runner.Hold(mock)

		waited := make(chan struct{})
		go func() {
			runner.Wait()
			close(waited)
		}()

		synctest.Wait()
		select {
		case <-waited:
			t.Fatal("Wait returned while a request held the runner")
		default:
		}

		start := time.Now()
		runner.OnComplete(mock, argsOf(mock), func(err error) { t.Error(err) })
		release()
		release() // a second release changes nothing

		<-waited
		require.True(t, ran.Load(), "Wait returned after the event")
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestEventRunner_CloseCancelsAndWaits(t *testing.T) {
	// Close cancels the events running, such as a webhook waiting for a slow target, and waits for them, so a server
	// closes at once. A closed runner starts nothing until Open, which a restarted server calls.
	synctest.Test(t, func(t *testing.T) {
		var started, late atomic.Int32
		var argsCtx, listenerCtx context.Context

		listeners := vlatte.NewListener()
		register(t, listeners, "waits", func(ctx context.Context, _ vlatte.OnComplete) error {
			started.Add(1)
			<-ctx.Done()

			return ctx.Err()
		})
		register(t, listeners, "counts", func(ctx context.Context, _ vlatte.OnComplete) error {
			late.Add(1)
			listenerCtx = ctx

			return ctx.Err()
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		waits := mockWith("waits")

		var got reported
		runner.OnComplete(waits, argsOf(waits), got.add)
		synctest.Wait()
		require.Equal(t, int32(1), started.Load())

		runner.Close()
		require.Empty(t, got.all(), "an event Close canceled isn't a failure")

		counts := mockWith("counts")
		runner.OnComplete(counts, argsOf(counts), got.add)
		runner.Hold(counts)()
		runner.Wait()
		require.Zero(t, late.Load(), "a closed runner starts nothing")

		runner.Close() // twice is fine
		runner.Open()
		runner.Open() // twice is fine

		argsFn := func(ctx context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
			argsCtx = ctx
			return completed{mock: counts, params: cfg.Parameters}
		}

		runner.OnComplete(counts, argsFn, got.add)
		runner.Wait()

		require.Equal(t, int32(1), late.Load(), "an open runner starts events again")
		require.Empty(t, got.all(), "with a live context")
		require.Same(t, argsCtx, listenerCtx, "argsFn gets the context the listener gets")
	})
}
