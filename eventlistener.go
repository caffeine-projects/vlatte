package vlatte

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
)

var ErrEventAlreadyRegistered = errors.New("vlatte: event already registered")

// EventArgs is the common argument type for all events.
// Event-specific interfaces (e.g. OnComplete) extend this.
type EventArgs interface {
	Kind() Kind
	Kit() Kit
	Parameters() Parameters
}

// EventListener is an interface for handling an event with type-safe args.
type EventListener[T EventArgs] interface {
	Run(ctx context.Context, args T) error
}

// EventPreparer is an optional interface of event listeners. Prepare checks the parameters of an event when the mock
// that names the listener is built, and those of global events when the server is built, so a mistake fails the mock
// instead of each event. It returns the parameters Run receives, such as decoded, and can compile their templates
// with engine, the server's.
type EventPreparer interface {
	Prepare(cfg EventConfig, engine TemplateEngine) (Parameters, error)
}

// EventFunc is a function that implements the [EventListener] interface.
type EventFunc[T EventArgs] func(ctx context.Context, args T) error

// Run implements the [EventListener] interface.
func (f EventFunc[T]) Run(ctx context.Context, args T) error {
	return f(ctx, args)
}

// EventConfig describes a named event and its parameters (e.g. for mock definitions).
type EventConfig struct {
	Name       string
	Parameters Parameters

	// Dir is where relative files of the parameters open from, such as the directory of the mock file that defines
	// the event. "" is the working directory.
	Dir string
}

type EventConfigs []EventConfig

func (e EventConfigs) Has(name string) bool {
	return slices.ContainsFunc(e, func(v EventConfig) bool {
		return v.Name == name
	})
}

type Events struct {
	OnComplete EventConfigs
}

// EventRegistry is a generic registry of named listeners for one event type.
// Use one registry per event kind (e.g. OnComplete, OnMatch) so new events can be added without changing this type.
type EventRegistry[T EventArgs] struct {
	mu        sync.RWMutex
	listeners map[string]EventListener[T]
}

// NewEventRegistry creates a new registry for an event type.
func NewEventRegistry[T EventArgs]() *EventRegistry[T] {
	return &EventRegistry[T]{
		listeners: make(map[string]EventListener[T]),
	}
}

// Register adds a listener under name. A name already registered keeps its listener: Register fails with
// ErrEventAlreadyRegistered.
func (r *EventRegistry[T]) Register(name string, evt EventListener[T]) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.listeners[name]; ok {
		return ErrEventAlreadyRegistered
	}
	r.listeners[name] = evt
	return nil
}

// Has reports whether a listener is registered for the given name.
func (r *EventRegistry[T]) Has(name string) bool {
	r.mu.RLock()
	_, ok := r.listeners[name]
	r.mu.RUnlock()
	return ok
}

// Prepare returns the parameters of cfg as the listener named cfg.Name prepares them, when it is an EventPreparer.
// Otherwise, or when no listener has that name, it returns them as they are.
func (r *EventRegistry[T]) Prepare(cfg EventConfig, engine TemplateEngine) (Parameters, error) {
	r.mu.RLock()
	listener, ok := r.listeners[cfg.Name]
	r.mu.RUnlock()

	if !ok {
		return cfg.Parameters, nil
	}

	if p, ok := listener.(EventPreparer); ok {
		return p.Prepare(cfg, engine)
	}

	return cfg.Parameters, nil
}

// Run looks up the listener by name and runs it with the given args.
// It is a no-op if the name is not registered.
func (r *EventRegistry[T]) Run(ctx context.Context, name string, args T) error {
	r.mu.RLock()
	listener, ok := r.listeners[name]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return listener.Run(ctx, args)
}

// Listener holds registries for each event kind.
// Add new event types by defining an interface (embedding EventArgs), adding a field here, and initializing it in NewEvents.
type Listener struct {
	OnComplete *EventRegistry[OnComplete]
}

// NewListener creates a new Listeners with all event registries initialized.
func NewListener() *Listener {
	return &Listener{
		OnComplete: NewEventRegistry[OnComplete](),
	}
}

// EventRunner runs the events of mocks, such as on_complete ones, each in a goroutine of its own, and cancels them
// when its server closes.
type EventRunner struct {
	Listeners *Listener
	Global    *Events

	mu     sync.Mutex
	ctx    context.Context    // made by the first request; Close cancels it, and so the events
	cancel context.CancelFunc // cancels ctx
	closed bool               // set by Close: a closed runner runs nothing
}

func NewEventRunner(listeners *Listener, global *Events) *EventRunner {
	if listeners == nil {
		listeners = NewListener()
	}
	if global == nil {
		global = &Events{}
	}

	return &EventRunner{Listeners: listeners, Global: global}
}

// OnComplete runs the on_complete events of mock, global events first, each in a goroutine of its own, and returns
// once they all have: an event neither waits for nor stops another, and the request waits for them all, so a test
// checks what they did right after the response. Each listener runs with what argsFn returns and a context with the
// values of ctx, the request's, such as a trace: the client leaving doesn't end it, and Close does. argsFn gets it
// too. onErr gets the error, or the panic, of each event that fails, unless Close canceled it, worded like the errors
// of mocks that name a listener that isn't registered. A closed runner runs nothing.
func (r *EventRunner) OnComplete(ctx context.Context, mock Mock, argsFn func(ctx context.Context, cfg EventConfig) OnComplete, onErr func(err error)) {
	closing, ok := r.context()
	if !ok {
		return
	}

	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	stop := context.AfterFunc(closing, cancel)
	defer stop()

	var wg sync.WaitGroup

	for i, def := range r.Global.OnComplete {
		wg.Go(func() { r.run(ctx, "global on_complete listener", i, def, argsFn, onErr) })
	}

	for i, def := range mock.Events().OnComplete {
		wg.Go(func() { r.run(ctx, "on_complete listener", i, def, argsFn, onErr) })
	}

	wg.Wait()
}

// Close cancels the context of the events running, so the requests waiting for them end, and runs no event after.
func (r *EventRunner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}

	r.ctx, r.cancel = nil, nil
}

// context returns the context Close cancels. A closed runner has none.
func (r *EventRunner) context() (context.Context, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, false
	}

	if r.ctx == nil {
		r.ctx, r.cancel = context.WithCancel(context.Background())
	}

	return r.ctx, true
}

// run runs the listener of def, at index i of the events that scope names.
func (r *EventRunner) run(ctx context.Context, scope string, i int, def EventConfig, argsFn func(context.Context, EventConfig) OnComplete, onErr func(error)) {
	name := fmt.Sprintf("%s %s at index %d", scope, def.Name, i)

	// A listener's panic, which the server's recovery caught when listeners ran on the handler's goroutine, would end
	// the process in a goroutine of its own.
	defer func() {
		if p := recover(); p != nil {
			onErr(fmt.Errorf("%s: panic: %v\n%s", name, p, debug.Stack()))
		}
	}()

	if !r.Listeners.OnComplete.Has(def.Name) {
		onErr(fmt.Errorf("%s is not registered", name))
		return
	}

	if err := r.Listeners.OnComplete.Run(ctx, def.Name, argsFn(ctx, def)); err != nil && ctx.Err() == nil {
		onErr(fmt.Errorf("%s: %w", name, err))
	}
}
