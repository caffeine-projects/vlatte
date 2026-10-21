package mockgrpc

import (
	"context"
	"slices"
	"uuid"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*StreamBuilder)(nil)

// StreamBuilder describes a mock. Build makes a new Mock from it each time, and leaves the builder as it was. The
// mocks built from one builder share its matchers; each gets a replier of its own from the reply.
type StreamBuilder struct {
	name         string
	expectations []*vlatte.Eval[*StreamValueSelectorIn]
	reply        StreamReplierFactory
}

// ServerStreamMethod starts a mock of a method, named by whole segments of its full method "/pkg.Service/Method": "Method",
// "Service/Method", "pkg.Service/Method" or the full method. A part of a name, such as "Meth", matches none.
func ServerStreamMethod(method string) *StreamBuilder {
	b := &StreamBuilder{}
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  methodName(method),
			Selector: streamSelectMethod,
		},
	)

	return b
}

func (b *StreamBuilder) Header(key string, m vlatte.Matcher) *StreamBuilder {
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  m,
			Selector: streamSelectHeader(key),
		},
	)

	return b
}

func (b *StreamBuilder) Field(path string, m vlatte.Matcher) *StreamBuilder {
	b.expectations = append(
		b.expectations,
		&vlatte.Eval[*StreamValueSelectorIn]{
			Matcher:  match.Field(path, m),
			Selector: streamSelectBody,
		},
	)
	return b
}

// Name defines a name for the mock.
// Useful for debugging.
func (b *StreamBuilder) Name(name string) *StreamBuilder {
	b.name = name
	return b
}

// Reply sets what the mock replies with. Build asks f for a new replier for each mock, so f can serve many mocks.
func (b *StreamBuilder) Reply(f StreamReplierFactory) *StreamBuilder {
	b.reply = f
	return b
}

// Build makes a new Mock for app from the builder, with an ID and a replier of its own, and leaves the builder as it
// was.
func (b *StreamBuilder) Build(app *Server) (*Mock, error) {
	reply, err := newReplier(b.reply, app)
	if err != nil {
		return nil, err
	}

	return &Mock{
		id:                 uuid.New().String(),
		name:               b.name,
		enabled:            true,
		streamReply:        reply,
		streamExpectations: slices.Clone(b.expectations),
	}, nil
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
