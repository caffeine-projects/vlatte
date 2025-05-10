package xhttp

import (
	"net/http"
)

// -- Function Reply

// Func returns a FunctionReply that builds a response using the given function.
func Func(fn func(http.ResponseWriter, *RequestValues) (*Response, error)) ReplyFunc {
	return func(w http.ResponseWriter, r *RequestValues) (*Response, error) {
		return fn(w, r)
	}
}

// -- HTTP Handler Reply

func Handler(h http.HandlerFunc) ReplyFunc {
	return func(w http.ResponseWriter, r *RequestValues) (*Response, error) {
		h(w, r.RawRequest)
		return nil, nil
	}
}
