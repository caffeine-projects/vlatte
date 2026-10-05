package mockgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*UnaryBuilder)(nil)

type UnaryBuilder struct {
	m *Mock
}

func UnaryMethod(method string) *UnaryBuilder {
	b := &UnaryBuilder{m: newMock()}
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  match.Contain(method),
			Selector: unarySelectMethod,
		},
	)

	return b
}

func (b *UnaryBuilder) Header(key string, m vlatte.Matcher) *UnaryBuilder {
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  m,
			Selector: unarySelectHeader(key),
		},
	)

	return b
}

func (b *UnaryBuilder) Field(path string, m vlatte.Matcher) *UnaryBuilder {
	b.m.unaryExpectations = append(
		b.m.unaryExpectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  match.Field(path, m),
			Selector: unarySelectBody,
		},
	)
	return b
}

func (b *UnaryBuilder) Reply(r vlatte.Replier[*UnaryRequestValues, any]) *UnaryBuilder {
	b.m.Reply = r
	return b
}

func (b *UnaryBuilder) Build(_ *Server) (*Mock, error) {
	return b.m, nil
}

func unarySelectMethod(_ context.Context, r *UnaryValueSelectorIn) any {
	return r.Info.FullMethod
}

func unarySelectHeader(k string) vlatte.Selector[*UnaryValueSelectorIn] {
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
