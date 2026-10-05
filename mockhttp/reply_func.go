package mockhttp

import (
	"context"
	"net/http"
)

var _ Replier = (*FuncReply)(nil)

// FuncReply is a replier that builds a response using the given function.
type FuncReply struct {
	fn func(http.ResponseWriter, *RequestValues) (*Response, error)
}

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (r *FuncReply) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	return r.fn(input.Writer, input.RequestValues)
}

// Func returns a Replier that builds a response using the given function.
func Func(fn func(http.ResponseWriter, *RequestValues) (*Response, error)) *FuncReply {
	return &FuncReply{fn: fn}
}

// Handler returns a Replier that delegates to the given http.HandlerFunc.
func Handler(h http.HandlerFunc) *FuncReply {
	return Func(func(w http.ResponseWriter, r *RequestValues) (*Response, error) {
		h(w, r.RawRequest)
		return nil, nil
	})
}
