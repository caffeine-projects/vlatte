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

func TestEventRunner_RunsEachEventOnItsOwnAndWaitsForAll(t *testing.T) {
	// Events ran one after the other, and the first error skipped the rest: a failing global webhook stopped the
	// mock's own. Each runs in a goroutine of its own now, so none holds up or skips another, and OnComplete returns
	// once they all have: the response waits for them, and a test checks what they did right after it.
	synctest.Test(t, func(t *testing.T) {
		var ran sync.Map
		ping, pong := make(chan struct{}), make(chan struct{})

		// ping and pong wait for each other: one after the other, they would never end.
		listeners := vlatte.NewListener()
		register(t, listeners, "ping", func(context.Context, vlatte.OnComplete) error {
			close(ping)
			<-pong
			ran.Store("ping", true)

			return nil
		})
		register(t, listeners, "pong", func(context.Context, vlatte.OnComplete) error {
			close(pong)
			<-ping
			ran.Store("pong", true)

			return nil
		})
		register(t, listeners, "failing", func(context.Context, vlatte.OnComplete) error {
			ran.Store("failing", true)
			return errors.New("target down")
		})
		register(t, listeners, "slow", func(context.Context, vlatte.OnComplete) error {
			time.Sleep(time.Second)
			ran.Store("slow", true)

			return nil
		})

		global := &vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "failing"}, {Name: "ping"}}}
		runner := vlatte.NewEventRunner(listeners, global)
		mock := mockWith("pong", "slow")

		var got reported
		start := time.Now()
		runner.OnComplete(mock, argsOf(mock), got.add)

		require.Equal(t, time.Second, time.Since(start), "OnComplete returned with the slowest event")
		for _, name := range []string{"ping", "pong", "failing", "slow"} {
			_, ok := ran.Load(name)
			require.True(t, ok, name)
		}

		require.Equal(t, []string{"global on_complete listener failing at index 0: target down"}, got.all())
	})
}

func TestEventRunner_ReportsUnregisteredNamesAndPanics(t *testing.T) {
	// A listener's panic was caught by the server's recovery while listeners ran on the handler's goroutine: in a
	// goroutine of its own, it would end the process. It's reported with its stack now, as an unregistered name is, and
	// the other events run.
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

		errs := got.all()
		require.Len(t, errs, 2)
		require.Equal(t, "on_complete listener nobody at index 1 is not registered", errs[0])
		require.True(t, strings.HasPrefix(errs[1], "on_complete listener panics at index 0: panic: boom\n"), errs[1])
		require.Contains(t, errs[1], "goroutine", "the stack of the panic")
		require.True(t, ran.Load())
	})
}

func TestEventRunner_CloseCancelsTheEventsRunning(t *testing.T) {
	// Close cancels the events running, such as a webhook waiting for a slow target, so the requests waiting for them
	// end, and a server closes at once. A closed runner runs nothing until Open, which a restarted server calls.
	synctest.Test(t, func(t *testing.T) {
		var late atomic.Int32
		var argsCtx, listenerCtx context.Context

		listeners := vlatte.NewListener()
		register(t, listeners, "waits", func(ctx context.Context, _ vlatte.OnComplete) error {
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
		done := make(chan struct{})
		go func() {
			runner.OnComplete(waits, argsOf(waits), got.add)
			close(done)
		}()

		synctest.Wait()
		select {
		case <-done:
			t.Fatal("OnComplete returned before its event")
		default:
		}

		runner.Close()
		<-done
		require.Empty(t, got.all(), "an event Close canceled isn't a failure")

		counts := mockWith("counts")
		runner.OnComplete(counts, argsOf(counts), got.add)
		require.Zero(t, late.Load(), "a closed runner runs nothing")

		runner.Close() // twice is fine
		runner.Open()
		runner.Open() // twice is fine

		argsFn := func(ctx context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
			argsCtx = ctx
			return completed{mock: counts, params: cfg.Parameters}
		}

		runner.OnComplete(counts, argsFn, got.add)

		require.Equal(t, int32(1), late.Load(), "an open runner runs events again")
		require.Empty(t, got.all(), "with a live context")
		require.Same(t, argsCtx, listenerCtx, "argsFn gets the context the listener gets")
	})
}
