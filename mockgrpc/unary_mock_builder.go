package mockgrpc

import (
	"context"
	"slices"
	"uuid"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*UnaryBuilder)(nil)

// UnaryBuilder describes a mock. Build makes a new Mock from it each time, and leaves the builder as it was. The
// mocks built from one builder share its matchers; each gets a replier of its own from the reply.
type UnaryBuilder struct {
	name         string
	expectations []*vlatte.Eval[*UnaryValueSelectorIn]
	reply        UnaryReplierFactory
}

func UnaryMethod(method string) *UnaryBuilder {
	b := &UnaryBuilder{}
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  match.Contain(method),
			Selector: unarySelectMethod,
		},
	)

	return b
}

func (b *UnaryBuilder) Header(key string, m vlatte.Matcher) *UnaryBuilder {
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  m,
			Selector: unarySelectHeader(key),
		},
	)

	return b
}

func (b *UnaryBuilder) Field(path string, m vlatte.Matcher) *UnaryBuilder {
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*UnaryValueSelectorIn]{
			Matcher:  match.Field(path, m),
			Selector: unarySelectBody,
		},
	)
	return b
}

// Name defines a name for the mock.
// Useful for debugging.
func (b *UnaryBuilder) Name(name string) *UnaryBuilder {
	b.name = name
	return b
}

// Reply sets what the mock replies with. Build asks f for a new replier for each mock, so f can serve many mocks.
func (b *UnaryBuilder) Reply(f UnaryReplierFactory) *UnaryBuilder {
	b.reply = f
	return b
}

// Build makes a new Mock for app from the builder, with an ID and a replier of its own, and leaves the builder as it
// was.
func (b *UnaryBuilder) Build(app *Server) (*Mock, error) {
	reply, err := newReplier(b.reply, app)
	if err != nil {
		return nil, err
	}

	return &Mock{
		id:                uuid.New().String(),
		name:              b.name,
		enabled:           true,
		unaryReply:        reply,
		unaryExpectations: slices.Clone(b.expectations),
	}, nil
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
