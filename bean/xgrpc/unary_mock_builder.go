package xgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/matcher"
)

var (
	_ kcore.Builder[*GRPCMock, *GRPCMockApp] = (*UnaryMockBuilder)(nil)
	_ GRPCMockBuilder                        = (*UnaryMockBuilder)(nil)
)

type UnaryMockBuilder struct {
	m *GRPCMock
}

func UnaryMethod(method string) *UnaryMockBuilder {
	b := &UnaryMockBuilder{m: newMock()}
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&kcore.Eval[*UnaryValueSelectorIn]{
			Matcher:  matcher.Contain(method),
			Selector: unarySelectMethod,
		},
	)

	return b
}

func (b *UnaryMockBuilder) Header(key string, m matcher.Matcher) *UnaryMockBuilder {
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&kcore.Eval[*UnaryValueSelectorIn]{
			Matcher:  m,
			Selector: unarySelectHeader(key),
		},
	)

	return b
}

func (b *UnaryMockBuilder) Field(path string, m matcher.Matcher) *UnaryMockBuilder {
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&kcore.Eval[*UnaryValueSelectorIn]{
			Matcher:  matcher.Field(path, m),
			Selector: unarySelectBody,
		},
	)
	return b
}

func (b *UnaryMockBuilder) Reply(r UnaryReply) *UnaryMockBuilder {
	b.m.Reply = r
	return b
}

func (b *UnaryMockBuilder) Build(_ *GRPCMockApp) (*GRPCMock, error) {
	return b.m, nil
}

func unarySelectMethod(_ context.Context, r *UnaryValueSelectorIn) any {
	return r.Info.FullMethod
}

func unarySelectHeader(k string) kcore.Selector[*UnaryValueSelectorIn] {
	return func(ctx context.Context, r *UnaryValueSelectorIn) any {
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

func unarySelectBody(_ context.Context, r *UnaryValueSelectorIn) any {
	return r.RequestMessage
}
