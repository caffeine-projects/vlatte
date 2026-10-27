package journal

import (
	"errors"
	"sync"
)

// EventErrors keeps the errors of the on_complete events of a request as they fail, such as webhooks, for its entry
// to tell. Events run at the same time, so Add is safe from many goroutines. The zero value is ready to use. A mock
// server keeps one for each request it journals.
type EventErrors struct {
	mu   sync.Mutex
	errs []error
}

// Add keeps err, the error of an event of the request.
func (e *EventErrors) Add(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.errs = append(e.errs, err)
}

// Tell sets the Message of entry to the errors kept, joined, unless it has one: the request failed on its own, which
// its message tells. It leaves entry as it is when no event failed.
func (e *EventErrors) Tell(entry *Entry) {
	e.mu.Lock()
	err := errors.Join(e.errs...)
	e.mu.Unlock()

	if entry.Message == "" && err != nil {
		entry.Message = err.Error()
	}
}
