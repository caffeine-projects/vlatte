package mockhttp

import (
	"errors"
	"net/http"
	"strconv"
)

var _ error = (*StatusError)(nil)

// ErrStatus is matched, with errors.Is, by every [StatusError].
var ErrStatus = errors.New("http: status")

// StatusError is an error a matcher returns to answer the request itself, such as a contract check that answers 400
// to a request its spec doesn't allow. It stops the search for a mock, as every matcher error does, and the server
// answers it on behalf of the matcher's mock: the mock counts the request as served, and the request is journaled as
// matched, not failed. The pipes, delay and on_complete events of the mock and of the server don't apply. A mock out of
// its turn, such as one used up, is passed over instead, as [vlatte.Mocks.Find] has it.
type StatusError struct {
	// Status is the status code of the answer, from 100 to 999: any other fails the request as a matching failure.
	Status int

	// Header holds the headers of the answer. Without a Content-Type, net/http sniffs one from Body.
	Header http.Header

	// Body is the body of the answer. It may be nil.
	Body []byte

	// Err tells why, such as the parts of the request a contract refuses. It may be nil.
	Err error
}

// Error returns the status, and why when Err is set.
func (e *StatusError) Error() string {
	msg := "http: status " + strconv.Itoa(e.Status)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}

	return msg
}

// Unwrap returns Err.
func (e *StatusError) Unwrap() error { return e.Err }

// Is reports whether target is [ErrStatus].
func (e *StatusError) Is(target error) bool { return target == ErrStatus }
