package xhttp

import (
	"context"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Replier[ReplyInput, *Response] = (*ReplyFunc)(nil)

// ReplyFunc is a replier that builds a response using the given function.
type ReplyFunc struct {
	fn func(http.ResponseWriter, *RequestValues) (*Response, error)
}

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (r *ReplyFunc) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	return r.fn(input.Writer, input.RequestValues)
}

// Func returns a Replier that builds a response using the given function.
func Func(fn func(http.ResponseWriter, *RequestValues) (*Response, error)) *ReplyFunc {
	return &ReplyFunc{fn: fn}
}

// Handler returns a Replier that delegates to the given http.HandlerFunc.
func Handler(h http.HandlerFunc) *ReplyFunc {
	return Func(func(w http.ResponseWriter, r *RequestValues) (*Response, error) {
		h(w, r.RawRequest)
		return nil, nil
	})
}
