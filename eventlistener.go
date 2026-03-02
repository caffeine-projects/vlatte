package vlatte

import (
	"context"
	"sync"
)

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

// Register adds or replaces a listener for the given name.
func (r *EventRegistry[T]) Register(name string, evt EventListener[T]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listeners == nil {
		r.listeners = make(map[string]EventListener[T])
	}
	r.listeners[name] = evt
}

// Has reports whether a listener is registered for the given name.
func (r *EventRegistry[T]) Has(name string) bool {
	r.mu.RLock()
	_, ok := r.listeners[name]
	r.mu.RUnlock()
	return ok
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

// Events holds registries for each event kind.
// Add new event types by defining an interface (embedding EventArgs), adding a field here, and initializing it in NewEvents.
type Events struct {
	OnComplete *EventRegistry[OnComplete]
}

// NewEvents creates a new Events with all event registries initialized.
func NewEvents() *Events {
	return &Events{
		OnComplete: NewEventRegistry[OnComplete](),
	}
}
