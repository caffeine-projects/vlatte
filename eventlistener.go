package vlatte

import (
	"context"
	"sync"
)

type EventArgs interface {
	Kind() Kind
	Kit() Kit
	Parameters() Parameters
}

type EventListener[T EventArgs] func(ctx context.Context, args T) error

type EventConfig struct {
	Name       string
	Parameters Parameters
}

type OnComplete interface {
	EventArgs

	Bean() Bean
}

type Events struct {
	mu         sync.RWMutex
	onComplete map[string]OnComplete
}

func NewEvents() *Events {
	return &Events{
		onComplete: make(map[string]OnComplete, 0),
	}
}

func (e *Events) OnComplete(name string, evt OnComplete) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onComplete[name] = evt
}
