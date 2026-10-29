package mockhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ ReplierFactory         = (*FuncReply)(nil)
	_ vlatte.SelfDescriptive = (*FuncReply)(nil)
	_ Replier                = funcReplier(nil)
)

// errFuncNotDescribed is why a mock with a Func or Handler reply can't be described: a mock file can't hold a function.
var errFuncNotDescribed = errors.New("reply: a Func or Handler reply can't be described as a mock file")

// FuncReply is a reply that builds the response using the given function. It is a ReplierFactory: every mock built
// with it calls the same function, so whatever the function keeps is shared by those mocks.
type FuncReply struct {
	fn func(http.ResponseWriter, *RequestValues) (*Response, error)
}

// Func returns a reply that builds the response using the given function. The context of its RawRequest also ends
// when a step of the mock cuts the answer. A mock file can't hold a function: describing a mock with it fails, so the
// admin API can't show the mock, nor save or store it.
func Func(fn func(http.ResponseWriter, *RequestValues) (*Response, error)) *FuncReply {
	return &FuncReply{fn: fn}
}

// Handler returns a reply that delegates to the given http.HandlerFunc. The context of its request also ends when a
// step of the mock cuts the answer. Describing a mock with it fails, as with [Func].
func Handler(h http.HandlerFunc) *FuncReply {
	return Func(func(w http.ResponseWriter, r *RequestValues) (*Response, error) {
		h(w, r.RawRequest)
		return nil, nil
	})
}

// NewReplier implements ReplierFactory.
func (r *FuncReply) NewReplier(*Server) (Replier, error) {
	return funcReplier(r.fn), nil
}

// Describe implements vlatte.SelfDescriptive. A mock file can't hold a function: encoding the description fails.
func (r *FuncReply) Describe() vlatte.Descriptor {
	return vlatte.DescribeFail("func", errFuncNotDescribed)
}

// funcReplier builds the responses of one mock with a function.
type funcReplier func(http.ResponseWriter, *RequestValues) (*Response, error)

// Reply implements Replier. The function's request has the reply's context, the one of the mock's steps.
func (f funcReplier) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	r := input.RequestValues
	if raw := r.RawRequest; raw != nil && raw.Context() != ctx {
		values := *r
		values.RawRequest = raw.WithContext(ctx)
		r = &values
	}

	return f(input.Writer, r)
}
