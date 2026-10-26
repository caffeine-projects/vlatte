package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
)

var _ Reply = (*BidiReply)(nil)

var (
	errNotBidi         = errors.New("a bidi reply answers bidi streaming methods only")
	errBidiRuleHeaders = errors.New("the header and trailer of a bidi answer are the bidi reply's: set them with " +
		"Bidi().Header and Bidi().Trailer")
)

// BidiReply describes the answer of a bidi streaming mock, message by message: each message the client sends gets the
// Response of the first rule whose matcher matches it, or of Otherwise. A Response that ends with a status other than
// OK ends the call there. Once the client closes its side, OnEnd's Response answers, and its status ends the call.
//
// A message no rule matches, without Otherwise, gets no answer.
type BidiReply struct {
	header    metadata.MD
	trailer   metadata.MD
	rules     []bidiRule
	otherwise *Response
	end       *Response
}

type bidiRule struct {
	matcher  vlatte.Matcher
	response *Response
}

// Bidi returns a BidiReply with no rules: one that answers nothing until the client closes its side, and then ends
// the call with OK.
func Bidi() *BidiReply {
	return &BidiReply{}
}

// On answers each message m matches with r. m reads the message as JSON, as [Builder.Field] reads a request:
// match.Field("message", match.Equal("hi")). The rules run in the order they were added.
func (b *BidiReply) On(m vlatte.Matcher, r *Response) *BidiReply {
	b.rules = append(b.rules, bidiRule{matcher: m, response: r})
	return b
}

// Otherwise answers each message no rule matches with r.
func (b *BidiReply) Otherwise(r *Response) *BidiReply {
	b.otherwise = r
	return b
}

// OnEnd answers with r once the client closed its side, and ends the call with r's status.
func (b *BidiReply) OnEnd(r *Response) *BidiReply {
	b.end = r
	return b
}

// Header adds metadata the answer starts with, sent as the call starts.
func (b *BidiReply) Header(key string, values ...string) *BidiReply {
	if b.header == nil {
		b.header = make(metadata.MD)
	}

	b.header.Append(key, values...)

	return b
}

// Trailer adds metadata the answer ends with.
func (b *BidiReply) Trailer(key string, values ...string) *BidiReply {
	if b.trailer == nil {
		b.trailer = make(metadata.MD)
	}

	b.trailer.Append(key, values...)

	return b
}

// NewReplier implements Reply.
func (b *BidiReply) NewReplier(app *Server, methods []protoreflect.MethodDescriptor) (Replier, error) {
	for _, md := range methods {
		if typeOf(md) != _bidiStream {
			return nil, fmt.Errorf("reply: %s is %s: %w", fullMethod(md), typeOf(md), errNotBidi)
		}
	}

	rep := &bidiReplier{header: b.header.Copy(), trailer: b.trailer.Copy()}

	for i, rule := range b.rules {
		r, err := rule.response.subReplier(app, methods)
		if err != nil {
			return nil, fmt.Errorf("reply: rule %d: %w", i, err)
		}

		rep.rules = append(rep.rules, bidiRuleReplier{matcher: rule.matcher, replier: r})
	}

	var err error
	if rep.otherwise, err = b.otherwise.subReplier(app, methods); err != nil {
		return nil, fmt.Errorf("reply: otherwise: %w", err)
	}

	if rep.end, err = b.end.subReplier(app, methods); err != nil {
		return nil, fmt.Errorf("reply: on end: %w", err)
	}

	return rep, nil
}

// subReplier returns the replier of a Response a bidi reply sends, nil for a nil Response. Its header and trailer
// would come too late, once the call started: they are the bidi reply's.
func (r *Response) subReplier(app *Server, methods []protoreflect.MethodDescriptor) (*responseReplier, error) {
	if r == nil {
		return nil, nil
	}

	if len(r.header) > 0 || len(r.trailer) > 0 {
		return nil, errBidiRuleHeaders
	}

	return r.replier(app, methods)
}

type bidiRuleReplier struct {
	matcher vlatte.Matcher
	replier *responseReplier
}

// bidiReplier answers the calls of one bidi streaming mock with a BidiReply.
type bidiReplier struct {
	header    metadata.MD
	trailer   metadata.MD
	rules     []bidiRuleReplier
	otherwise *responseReplier
	end       *responseReplier
}

// Reply implements Replier.
func (r *bidiReplier) Reply(ctx context.Context, call *Call) error {
	if err := grpc.SendHeader(ctx, r.header); err != nil {
		return err
	}

	if err := grpc.SetTrailer(ctx, r.trailer); err != nil {
		return err
	}

	for {
		msg, err := call.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		b, err := call.server.resolver.encode(msg)
		if err != nil {
			return err
		}

		rep, err := r.answerOf(ctx, b)
		if err != nil {
			return err
		}

		if rep == nil {
			continue
		}

		// A status other than OK ends the call there.
		if err := rep.send(ctx, call); err != nil {
			return err
		}
	}

	if r.end == nil {
		return nil
	}

	return r.end.send(ctx, call)
}

// answerOf returns the replier of the message b, as JSON: the first rule's that matches it, else Otherwise's, nil for
// none.
func (r *bidiReplier) answerOf(ctx context.Context, b []byte) (*responseReplier, error) {
	for i, rule := range r.rules {
		ok, err := rule.matcher.Match(vlatte.NewMatcherArg(ctx, nil, string(b)))
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}

		if ok {
			return rule.replier, nil
		}
	}

	return r.otherwise, nil
}
