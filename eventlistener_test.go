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
		runner.OnComplete(t.Context(), mock, argsOf(mock), got.add)

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
		runner.OnComplete(t.Context(), mock, argsOf(mock), got.add)

		errs := got.all()
		require.Len(t, errs, 2)
		require.Equal(t, "on_complete listener nobody at index 1 is not registered", errs[0])
		require.True(t, strings.HasPrefix(errs[1], "on_complete listener panics at index 0: panic: boom\n"), errs[1])
		require.Contains(t, errs[1], "goroutine", "the stack of the panic")
		require.True(t, ran.Load())
	})
}

func TestEventRunner_RunsTheListenerAnEventCarries(t *testing.T) {
	// An event set in Go, such as a callback, carries its listener: it runs with no registration, even under a name a
	// registered listener has, and its error and panic are reported under its name, as a registered one's are.
	synctest.Test(t, func(t *testing.T) {
		var got reported

		listeners := vlatte.NewListener()
		register(t, listeners, "callback", func(context.Context, vlatte.OnComplete) error {
			got.add(errors.New("the registered listener ran"))
			return nil
		})

		params := vlatte.Parameters(map[string]any{"k": "v"})
		gotParams := make(chan vlatte.Parameters, 2)

		mock := newFakeMock()
		mock.events.OnComplete = vlatte.EventConfigs{
			{Name: "callback", Parameters: params, Listener: vlatte.EventFunc[vlatte.OnComplete](
				func(_ context.Context, e vlatte.OnComplete) error {
					gotParams <- e.Parameters()
					return nil
				})},
			{Name: "callback", Listener: vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
				return errors.New("failed")
			})},
			{Name: "callback", Listener: vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
				panic("boom")
			})},
		}

		vlatte.NewEventRunner(nil, nil).OnComplete(t.Context(), mock, argsOf(mock), got.add)
		vlatte.NewEventRunner(listeners, nil).OnComplete(t.Context(), mock, argsOf(mock), got.add)

		errs := got.all()
		require.Len(t, errs, 4, "each runner reports the error and the panic, and no registered listener runs")
		require.Equal(t, "on_complete listener callback at index 1: failed", errs[0])
		require.Equal(t, "on_complete listener callback at index 1: failed", errs[1])
		require.True(t, strings.HasPrefix(errs[2], "on_complete listener callback at index 2: panic: boom\n"), errs[2])
		require.True(t, strings.HasPrefix(errs[3], "on_complete listener callback at index 2: panic: boom\n"), errs[3])
		require.Equal(t, params, <-gotParams, "argsFn gets the event, parameters included")
		require.Equal(t, params, <-gotParams)
	})
}

func TestEventRunner_CloseCancelsTheEventsRunning(t *testing.T) {
	// Close cancels the events running, such as a webhook waiting for a slow target, so the requests waiting for them
	// end, and a server closes at once. A closed runner runs nothing after.
	synctest.Test(t, func(t *testing.T) {
		var counted atomic.Int32
		var argsCtx, listenerCtx context.Context

		listeners := vlatte.NewListener()
		register(t, listeners, "waits", func(ctx context.Context, _ vlatte.OnComplete) error {
			<-ctx.Done()
			return ctx.Err()
		})
		register(t, listeners, "counts", func(ctx context.Context, _ vlatte.OnComplete) error {
			counted.Add(1)
			listenerCtx = ctx

			return ctx.Err()
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		counts := mockWith("counts")

		var got reported
		argsFn := func(ctx context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
			argsCtx = ctx
			return completed{mock: counts, params: cfg.Parameters}
		}

		runner.OnComplete(t.Context(), counts, argsFn, got.add)

		require.Equal(t, int32(1), counted.Load(), "an open runner runs events")
		require.Empty(t, got.all(), "with a live context")
		require.Same(t, argsCtx, listenerCtx, "argsFn gets the context the listener gets")

		waits := mockWith("waits")
		done := make(chan struct{})
		go func() {
			runner.OnComplete(t.Context(), waits, argsOf(waits), got.add)
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

		runner.OnComplete(t.Context(), counts, argsOf(counts), got.add)
		require.Equal(t, int32(1), counted.Load(), "a closed runner runs nothing")

		runner.Close() // twice is fine
	})
}

func TestEventRunner_EventsKeepTheValuesOfTheRequestContext(t *testing.T) {
	// Events ran with a context of their own, without the values of the request's, such as a trace a middleware put
	// there. They get its values now, but not its end: a client that leaves cancels none of them.
	synctest.Test(t, func(t *testing.T) {
		type traceKey struct{}

		var trace any
		var ctxErr error

		listeners := vlatte.NewListener()
		register(t, listeners, "reads", func(ctx context.Context, _ vlatte.OnComplete) error {
			trace, ctxErr = ctx.Value(traceKey{}), ctx.Err()
			return nil
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		mock := mockWith("reads")

		ctx, leave := context.WithCancel(context.WithValue(t.Context(), traceKey{}, "trace-1"))
		leave() // the client left before the events ran

		runner.OnComplete(ctx, mock, argsOf(mock), func(err error) { t.Error(err) })

		require.Equal(t, "trace-1", trace)
		require.NoError(t, ctxErr, "the client leaving cancels no event")
	})
}

func TestEventRunner_EventsAfterTheResponseDontHoldIt(t *testing.T) {
	// An app that needs its callback once it has its answer, got it only after the response,
	// which waited for every event. An event set to run after the response doesn't hold it now: OnComplete returns
	// while it still runs, and it keeps the values of the request's context, not its end. The others still hold it.
	synctest.Test(t, func(t *testing.T) {
		type traceKey struct{}

		release := make(chan struct{})
		var waited atomic.Bool
		var trace any

		listeners := vlatte.NewListener()
		register(t, listeners, "late", func(ctx context.Context, _ vlatte.OnComplete) error {
			<-release
			trace = ctx.Value(traceKey{})

			return ctx.Err()
		})
		register(t, listeners, "failing", func(context.Context, vlatte.OnComplete) error {
			<-release
			return errors.New("target down")
		})
		register(t, listeners, "waited", func(context.Context, vlatte.OnComplete) error {
			time.Sleep(time.Second)
			waited.Store(true)

			return nil
		})

		global := &vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "failing", AfterResponse: true}}}
		runner := vlatte.NewEventRunner(listeners, global)
		mock := newFakeMock()
		mock.events.OnComplete = vlatte.EventConfigs{{Name: "waited"}, {Name: "late", AfterResponse: true}}

		ctx, leave := context.WithCancel(context.WithValue(t.Context(), traceKey{}, "trace-1"))

		var got reported
		runner.OnComplete(ctx, mock, argsOf(mock), got.add)
		leave() // the client left once it had its answer

		require.True(t, waited.Load(), "the response waits for the events without the flag")
		require.Empty(t, got.all(), "the events after the response still run")

		close(release)
		synctest.Wait()

		require.Equal(t, "trace-1", trace)
		require.Equal(t, []string{"global on_complete listener failing at index 0: target down"}, got.all(),
			"the client leaving cancels none, and a failure is reported as any event's")
	})
}

func TestEventRunner_CloseCancelsAndWaitsForTheEventsAfterTheResponse(t *testing.T) {
	// No request waits for an event that runs after the response: Close cancels it, such as a webhook waiting for a
	// slow target, and returns once it ended, so a server closes with none still running. A closed runner starts none.
	synctest.Test(t, func(t *testing.T) {
		var started atomic.Int32
		var ended atomic.Bool

		listeners := vlatte.NewListener()
		register(t, listeners, "waits", func(ctx context.Context, _ vlatte.OnComplete) error {
			started.Add(1)
			<-ctx.Done()
			time.Sleep(time.Second) // what it does once canceled, such as closing a connection
			ended.Store(true)

			return ctx.Err()
		})

		runner := vlatte.NewEventRunner(listeners, nil)
		mock := newFakeMock()
		mock.events.OnComplete = vlatte.EventConfigs{{Name: "waits", AfterResponse: true}}

		var got reported
		runner.OnComplete(t.Context(), mock, argsOf(mock), got.add)

		synctest.Wait()
		require.Equal(t, int32(1), started.Load())

		runner.Close()
		require.True(t, ended.Load(), "Close returned before the event ended")
		require.Empty(t, got.all(), "an event Close canceled isn't a failure")

		runner.OnComplete(t.Context(), mock, argsOf(mock), got.add)
		synctest.Wait()
		require.Equal(t, int32(1), started.Load(), "a closed runner starts nothing")
	})
}

// preparer is an event listener that prepares its parameters with prepare.
type preparer struct {
	prepare func(cfg vlatte.EventConfig, engine vlatte.TemplateEngine) (vlatte.Parameters, error)
}

func (p preparer) Run(context.Context, vlatte.OnComplete) error { return nil }

func (p preparer) Prepare(cfg vlatte.EventConfig, engine vlatte.TemplateEngine) (vlatte.Parameters, error) {
	return p.prepare(cfg, engine)
}

func TestEventRegistry_Prepare(t *testing.T) {
	engine := vlatte.MustNewGoTemplateEngine()
	cfg := vlatte.EventConfig{Name: "hook", Parameters: map[string]any{"url": "x"}, Dir: "mocks"}

	t.Run("a preparer gets the config and the engine, and its parameters are the event's", func(t *testing.T) {
		r := vlatte.NewEventRegistry[vlatte.OnComplete]()
		require.NoError(t, r.Register("hook", preparer{prepare: func(got vlatte.EventConfig, e vlatte.TemplateEngine) (vlatte.Parameters, error) {
			require.Equal(t, cfg, got)
			require.Same(t, engine, e)

			return "prepared", nil
		}}))

		params, err := r.Prepare(cfg, engine)
		require.NoError(t, err)
		require.Equal(t, "prepared", params)
	})

	t.Run("a preparer's error is returned", func(t *testing.T) {
		r := vlatte.NewEventRegistry[vlatte.OnComplete]()
		require.NoError(t, r.Register("hook", preparer{prepare: func(vlatte.EventConfig, vlatte.TemplateEngine) (vlatte.Parameters, error) {
			return nil, errors.New("hook: bad url")
		}}))

		_, err := r.Prepare(cfg, engine)
		require.EqualError(t, err, "hook: bad url")
	})

	t.Run("other listeners and unknown names keep the parameters", func(t *testing.T) {
		r := vlatte.NewEventRegistry[vlatte.OnComplete]()
		require.NoError(t, r.Register("hook", vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })))

		for _, name := range []string{"hook", "nobody"} {
			params, err := r.Prepare(vlatte.EventConfig{Name: name, Parameters: cfg.Parameters}, engine)
			require.NoError(t, err)
			require.Equal(t, cfg.Parameters, params)
		}
	})
}
