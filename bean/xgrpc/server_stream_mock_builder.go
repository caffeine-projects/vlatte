package xgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte/std"
	"github.com/caffeine-projects/vlatte/filter"
)

var (
	_ std.Builder[*GRPCMock, *GRPCMockApp] = (*ServerStreamMockBuilder)(nil)
	_ GRPCMockBuilder                      = (*ServerStreamMockBuilder)(nil)
)

type ServerStreamMockBuilder struct {
	m *GRPCMock
}

func ServerStreamMethod(method string) *ServerStreamMockBuilder {
	b := &ServerStreamMockBuilder{m: newMock()}
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&std.Eval[*StreamValueSelectorIn]{
			Matcher:  filter.Contain(method),
			Selector: streamSelectMethod,
		},
	)

	return b
}

func (b *ServerStreamMockBuilder) Header(key string, m filter.Matcher) *ServerStreamMockBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&std.Eval[*StreamValueSelectorIn]{
			Matcher:  m,
			Selector: streamSelectHeader(key),
		},
	)

	return b
}

func (b *ServerStreamMockBuilder) Field(path string, m filter.Matcher) *ServerStreamMockBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&std.Eval[*StreamValueSelectorIn]{
			Matcher:  filter.Field(path, m),
			Selector: streamSelectBody,
		},
	)
	return b
}

func (b *ServerStreamMockBuilder) Reply(r ServerStreamReply) *ServerStreamMockBuilder {
	b.m.Reply = r
	return b
}

func (b *ServerStreamMockBuilder) Build(_ *GRPCMockApp) (*GRPCMock, error) {
	return b.m, nil
}

func streamSelectMethod(_ context.Context, r *StreamValueSelectorIn) any {
	return r.Info.FullMethod
}

func streamSelectHeader(k string) std.Selector[*StreamValueSelectorIn] {
	return func(ctx context.Context, r *StreamValueSelectorIn) any {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil
		}

		v := md.Get(k)
		if len(v) == 1 {
			return v[0]
		}

		return v
	}
}

func streamSelectBody(_ context.Context, r *StreamValueSelectorIn) any {
	return r.RequestMessage
}
