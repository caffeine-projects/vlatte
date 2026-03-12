package xgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
)

var (
	_ vlatte.Grinder[*GRPCMock, *GRPCMockApp] = (*ServerStreamMockBuilder)(nil)
	_ GRPCMockBuilder                         = (*ServerStreamMockBuilder)(nil)
)

type ServerStreamMockBuilder struct {
	m *GRPCMock
}

func ServerStreamMethod(method string) *ServerStreamMockBuilder {
	b := &ServerStreamMockBuilder{m: newMock()}
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  filter.Contain(method),
			Selector: streamSelectMethod,
		},
	)

	return b
}

func (b *ServerStreamMockBuilder) Header(key string, m vlatte.Filter) *ServerStreamMockBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  m,
			Selector: streamSelectHeader(key),
		},
	)

	return b
}

func (b *ServerStreamMockBuilder) Field(path string, m vlatte.Filter) *ServerStreamMockBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  filter.Field(path, m),
			Selector: streamSelectBody,
		},
	)
	return b
}

func (b *ServerStreamMockBuilder) Reply(r vlatte.Replier[*StreamRequestValues, any]) *ServerStreamMockBuilder {
	b.m.Reply = r
	return b
}

func (b *ServerStreamMockBuilder) Grind(_ *GRPCMockApp) (*GRPCMock, error) {
	return b.m, nil
}

func streamSelectMethod(_ context.Context, r *StreamValueSelectorIn) any {
	return r.Info.FullMethod
}

func streamSelectHeader(k string) vlatte.Selector[*StreamValueSelectorIn] {
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
