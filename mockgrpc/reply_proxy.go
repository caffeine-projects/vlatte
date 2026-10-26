package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ Reply     = (*ProxyReply)(nil)
	_ Replier   = (*proxyReplier)(nil)
	_ io.Closer = (*proxyReplier)(nil)
)

var errNoTarget = errors.New("reply_proxy: no target: name the server to proxy to, such as localhost:50051")

// _debugKeys are the debug header keys, as metadata holds them: a target that is a mock server sets its own, which
// the mock's replace.
var _debugKeys = []string{
	strings.ToLower(vlatte.MatchedMockIDHeader),
	strings.ToLower(vlatte.MatchedMockHitsHeader),
	strings.ToLower(vlatte.MatchedMockNameHeader),
}

// ProxyReply answers each call with the answer of another gRPC server, the target: the mock sends it the call, its
// metadata and messages, and sends the client the target's header, messages, trailer and status, as they come. It
// serves every call type: a bidi call goes on message by message, in both directions. Use From to make one. Each mock
// built with it gets a connection of its own, which closes when the mock leaves the server, or the server closes.
//
// A status of the target's is the mock's answer, as a status of Status's is. A target the mock can't reach fails the
// call: the client gets Internal with the vlatte-error trailer.
type ProxyReply struct {
	target      string
	header      metadata.MD
	remove      []string
	dialOptions []grpc.DialOption
}

// From returns a ProxyReply to target, a gRPC target such as localhost:50051 or dns:///api.example.com:443, as
// grpc.NewClient takes it. It connects without TLS: see ProxyReply.DialOptions.
func From(target string) *ProxyReply {
	return &ProxyReply{target: target}
}

// Header adds metadata the target gets, which replaces the client's values of that key: a token for the target in
// place of the client's.
func (r *ProxyReply) Header(key string, values ...string) *ProxyReply {
	if r.header == nil {
		r.header = make(metadata.MD)
	}

	r.header.Append(key, values...)

	return r
}

// RemoveHeaders removes the client's metadata of those keys from what the target gets.
func (r *ProxyReply) RemoveHeaders(keys ...string) *ProxyReply {
	r.remove = append(r.remove, keys...)
	return r
}

// DialOptions sets options of the connection to the target, after the reply's own, which connects without TLS: such
// as grpc.WithTransportCredentials for a target that serves TLS. A mock with them has no mock file description: the
// admin API describes it without its reply.
func (r *ProxyReply) DialOptions(opts ...grpc.DialOption) *ProxyReply {
	r.dialOptions = append(r.dialOptions, opts...)
	return r
}

// NewReplier implements Reply. It makes the connection of the mock, which connects on its first call.
func (r *ProxyReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return r.replier()
}

// replier makes the replier of NewReplier.
func (r *ProxyReply) replier() (*proxyReplier, error) {
	if r.target == "" {
		return nil, errNoTarget
	}

	opts := slices.Concat([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, r.dialOptions)

	conn, err := grpc.NewClient(r.target, opts...)
	if err != nil {
		return nil, fmt.Errorf("reply_proxy: target %q: %w", r.target, err)
	}

	remove := make([]string, len(r.remove))
	for i, k := range r.remove {
		remove[i] = strings.ToLower(k)
	}

	return &proxyReplier{target: r.target, conn: conn, header: r.header.Copy(), remove: remove}, nil
}

// proxyReplier sends the calls of one mock to the target, and answers them with the target's answers.
type proxyReplier struct {
	target string
	conn   *grpc.ClientConn
	header metadata.MD
	remove []string
}

// Reply implements Replier.
func (p *proxyReplier) Reply(ctx context.Context, call *Call) error {
	desc := &grpc.StreamDesc{
		StreamName:    string(call.desc.Name()),
		ServerStreams: call.isServerStream(),
		ClientStreams: call.isClientStream(),
	}

	// The call to the target ends with the client's: when it leaves, or its deadline passes.
	var from peer.Peer

	cs, err := p.conn.NewStream(metadata.NewOutgoingContext(ctx, p.outgoing(call.header)), desc, call.method, grpc.Peer(&from))
	if err != nil {
		return p.unreached(err)
	}

	p.send(call, cs)

	// The target's header goes out as it comes, before its first message. A call it ended at once has none: its
	// status, and trailer, answer.
	if header, _ := cs.Header(); header != nil {
		if err := grpc.SendHeader(ctx, incomingMD(header)); err != nil {
			return err
		}
	}

	err = p.receive(call, cs)

	if err := grpc.SetTrailer(ctx, incomingMD(cs.Trailer())); err != nil {
		return err
	}

	// A call that never reached the target has no peer: its status is grpc's, not the target's.
	if err != nil && from.Addr == nil {
		return p.unreached(err)
	}

	return err
}

// Close closes the connection to the target. A call it carries ends.
func (p *proxyReplier) Close() error {
	return p.conn.Close()
}

// send sends the target the messages of the client: the one of a unary or server streaming call, all those of a client
// streaming call, and, from a goroutine, those of a bidi call as the client sends them, while the target answers. A
// message the target doesn't take, because it ended the call, ends the sending: the status tells why.
func (p *proxyReplier) send(call *Call, cs grpc.ClientStream) {
	if call.isClientStream() && call.isServerStream() {
		// The goroutine ends once the client closes its side, or the call ends, which ends Recv.
		go pump(call, cs)
		return
	}

	for _, msg := range call.Messages() {
		if cs.SendMsg(msg) != nil {
			return
		}
	}

	_ = cs.CloseSend()
}

// pump sends the target each message the client of a bidi call sends, until the client closes its side, which closes
// the target's.
func pump(call *Call, cs grpc.ClientStream) {
	for {
		msg, err := call.Recv()
		if errors.Is(err, io.EOF) {
			_ = cs.CloseSend()
			return
		}

		if err != nil || cs.SendMsg(msg) != nil {
			return
		}
	}
}

// receive sends the client the target's messages, as they come, and returns the status the target ended the call with:
// nil for OK.
func (p *proxyReplier) receive(call *Call, cs grpc.ClientStream) error {
	for {
		msg := call.NewOutput()

		err := cs.RecvMsg(msg)
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		if err := call.Send(msg); err != nil {
			return err
		}

		// A call that answers one message ends with it.
		if !call.isServerStream() {
			return nil
		}
	}
}

// outgoing returns the metadata the target gets: the client's, but the keys gRPC sets of its own, such as
// :authority and grpc-timeout, and those RemoveHeaders names, with Header's in place of the client's values.
func (p *proxyReplier) outgoing(md metadata.MD) metadata.MD {
	out := make(metadata.MD, len(md)+len(p.header))

	for k, v := range md {
		if !reservedKey(k) && !slices.Contains(p.remove, k) {
			out[k] = slices.Clone(v)
		}
	}

	for k, v := range p.header {
		out[k] = slices.Clone(v)
	}

	return out
}

// unreached is the failure of a call that never reached the target, err its status: an error without one, so the
// client tells it from an answer of the target's.
func (p *proxyReplier) unreached(err error) error {
	return fmt.Errorf("reply_proxy: target %s: %s", p.target, status.Convert(err).Message())
}

// incomingMD returns the metadata of the target's answer the client gets: all of it, but the debug headers of a target
// that is a mock server, which the mock's own replace.
func incomingMD(md metadata.MD) metadata.MD {
	out := md.Copy()
	for _, k := range _debugKeys {
		delete(out, k)
	}

	return out
}

// reservedKey reports whether gRPC sets the metadata key k of its own on each call, such as :authority, content-type
// and grpc-timeout: a call it sends takes none of the client's.
func reservedKey(k string) bool {
	return strings.HasPrefix(k, ":") || strings.HasPrefix(k, "grpc-") || k == "content-type" || k == "user-agent" || k == "te"
}
