package mockgrpc

import (
	"context"

	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	_ Reply   = sharedReply{}
	_ Replier = sharedReply{}
)

// Shared returns a Reply that gives r to every mock built with it: those mocks share r, and whatever it keeps. Use it
// for a Replier that keeps no state, such as one from another package. A Replier that keeps state, such as a counter,
// should come from a Reply of its own that makes one for each mock. The mocks never close r: when it implements
// io.Closer, closing it is the caller's. Mock files have no shared reply: describing a mock with it fails, so the admin
// API can't show the mock, nor save it.
func Shared(r Replier) Reply {
	return sharedReply{r: r}
}

// sharedReply gives every mock the same Replier. It is the replier of its mocks itself, which never closes r.
type sharedReply struct {
	r Replier
}

// NewReplier implements Reply. A nil r makes no replier, which fails the build.
func (s sharedReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	if s.r == nil {
		return nil, nil
	}

	return s, nil
}

// Reply implements Replier.
func (s sharedReply) Reply(ctx context.Context, call *Call) error {
	return s.r.Reply(ctx, call)
}
