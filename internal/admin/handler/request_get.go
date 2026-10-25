package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte/journal"
)

// GetRequest returns the request of id. It needs a server that keeps a journal.
func (o *Operations) GetRequest(id string) (*journal.Entry, error) {
	if o.journal == nil {
		return nil, notSupported(_lackingJournal)
	}

	entry, err := o.journal.Get(id)
	if errors.Is(err, journal.ErrNotFound) {
		return nil, &problemError{
			status: http.StatusNotFound, code: _codeRequestNotFound,
			detail: fmt.Sprintf("the journal holds no request of the id %q", id),
		}
	}

	if err != nil {
		return nil, err
	}

	return entry, nil
}
