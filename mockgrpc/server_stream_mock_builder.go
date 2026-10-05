package mockgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*StreamBuilder)(nil)

type StreamBuilder struct {
	m *Mock
}

func ServerStreamMethod(method string) *StreamBuilder {
	b := &StreamBuilder{m: newMock()}
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  match.Contain(method),
			Selector: streamSelectMethod,
		},
	)

	return b
}

func (b *StreamBuilder) Header(key string, m vlatte.Matcher) *StreamBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  m,
			Selector: streamSelectHeader(key),
		},
	)

	return b
}

func (b *StreamBuilder) Field(path string, m vlatte.Matcher) *StreamBuilder {
	b.m.streamExpectations = append(
		b.m.streamExpectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  match.Field(path, m),
			Selector: streamSelectBody,
		},
	)
	return b
}

func (b *StreamBuilder) Reply(r vlatte.Replier[*StreamRequestValues, any]) *StreamBuilder {
	b.m.Reply = r
	return b
}

func (b *StreamBuilder) Build(_ *Server) (*Mock, error) {
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
