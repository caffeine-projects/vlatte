package journal

import (
	"errors"
	"sync"
)

// Draft is the entry of a request a mock server is serving, which the server fills in as the request goes and appends
// once the request ends. It keeps the errors of the request's on_complete events that fail, such as webhooks, for the
// entry to tell. Its methods are safe from many goroutines, as the events of a request run at the same time.
type Draft struct {
	journal  *Journal
	mu       sync.Mutex
	entry    Entry
	events   []error
	appended bool
}

// Draft starts the draft of entry, which [Draft.Append] appends to j. A nil j keeps no journal: Append returns
// [ErrNoJournal].
func (j *Journal) Draft(entry Entry) *Draft {
	return &Draft{journal: j, entry: entry}
}

// ID returns the request id of the entry.
func (d *Draft) ID() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.entry.ID
}

// Update runs set on the entry, which must not call the methods of d. What it sets once the entry is appended is left
// out of the journal.
func (d *Draft) Update(set func(e *Entry)) {
	d.mu.Lock()
	defer d.mu.Unlock()

	set(&d.entry)
}

// EventFailed keeps err, the error of an on_complete event of the request. An event that fails once the entry is
// appended is left out of the journal.
func (d *Draft) EventFailed(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.events = append(d.events, err)
}

// Append runs end on the entry, which must not call the methods of d, and appends it to the journal, as
// [Journal.Append] does. The entry's Message tells the events that failed, joined, unless it has a message of its own:
// the request failed, which its message tells. Only the first Append counts: another appends nothing and returns nil.
func (d *Draft) Append(end func(e *Entry)) error {
	d.mu.Lock()

	if d.appended {
		d.mu.Unlock()
		return nil
	}

	d.appended = true
	end(&d.entry)

	entry := d.entry
	if err := errors.Join(d.events...); entry.Message == "" && err != nil {
		entry.Message = err.Error()
	}

	d.mu.Unlock()

	return d.journal.Append(&entry)
}
