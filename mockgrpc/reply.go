package mockgrpc

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
)

var (
	errNoReply   = errors.New("mock: no reply set: use Reply")
	errNoReplier = errors.New("reply: the factory made no replier")
)

// _failureKey is vlatte.HeaderFailure as a metadata key, which gRPC wants lowercase.
var _failureKey = strings.ToLower(vlatte.HeaderFailure)

// Reply is what a mock answers the calls it matches with: [Message], [Messages], [JSON], [Template], [Status], [Bidi],
// [Seq], [Rand], [Echo], [Shared] and [Func] make one. [Builder.Build] asks it for a Replier for each mock, given the
// methods the mock serves, so it checks there what it can, such as that its messages are of the type the methods
// answer with.
type Reply interface {
	NewReplier(app *Server, methods []protoreflect.MethodDescriptor) (Replier, error)
}

// Replier answers the calls of one mock. Reply sends the answer through call: the messages with [Call.Send], and the
// header and trailer with grpc.SetHeader, grpc.SendHeader and grpc.SetTrailer on ctx. An error with a gRPC status,
// such as status.Error makes, ends the call with that status: it is the mock's answer.
//
// An error that matches [vlatte.ErrAbort] or [vlatte.ErrReset] cuts the call, as a server that crashes would: the mock
// counts the call and runs its events, then the server cuts the call's connection after the messages of a stream
// Reply sent, before any status, so the client reads Unavailable and no trailer. ErrReset resets a TCP connection, so
// the client reads ECONNRESET, and ErrAbort ends it. grpc can't reset one stream from a handler, so the other calls on
// the connection end too. A server whose ServerOptions set grpc.Creds can't cut its connections: the call fails.
//
// Any other error is a failure, which the client gets as Internal with the vlatte-error trailer.
type Replier interface {
	Reply(ctx context.Context, call *Call) error
}

// ReplierFunc is a Replier of a function, and a Reply that gives every mock that function.
type ReplierFunc func(ctx context.Context, call *Call) error

// Func returns a Reply that answers each call with fn, which sends the answer through call: see [Replier]. A unary or
// client streaming call whose fn sends no message answers with an empty message of the method's output type.
func Func(fn func(ctx context.Context, call *Call) error) ReplierFunc {
	return fn
}

// Reply implements Replier.
func (f ReplierFunc) Reply(ctx context.Context, call *Call) error {
	return f(ctx, call)
}

// NewReplier implements Reply.
func (f ReplierFunc) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return f, nil
}

// newReplier asks r for the replier of a mock of app, which serves methods. A reply that makes no replier fails the
// build.
func newReplier(r Reply, app *Server, methods []protoreflect.MethodDescriptor) (Replier, error) {
	replier, err := r.NewReplier(app, methods)
	if err != nil {
		return nil, err
	}

	if replier == nil {
		return nil, errNoReplier
	}

	return replier, nil
}

// fail answers a call the server failed to serve, or no mock matched: it adds failure, a vlatte.Failure* value, to
// the trailer of the call, so a client tells the answer from a mock's, and returns a status error with code c.
func fail(ctx context.Context, failure string, c codes.Code, format string, a ...any) error {
	_ = grpc.SetTrailer(ctx, metadata.Pairs(_failureKey, failure))
	return status.Errorf(c, format, a...)
}

// debugHeader is the header metadata naming mock, the mock that matched a call: its ID, hits, its hits with this call,
// and its name, when it has one.
func debugHeader(mock *Mock, hits int64) metadata.MD {
	md := metadata.Pairs(
		vlatte.HeaderMatchedMockID, mock.ID(),
		vlatte.HeaderMatchedMockHits, strconv.FormatInt(hits, 10),
	)

	if name := mock.Name(); name != "" {
		md.Set(vlatte.HeaderMatchedMockName, name)
	}

	return md
}

// replyError is the answer to a call whose mock replied err. An error with a gRPC status is the mock's answer, such
// as a status set with Status, so it is returned as is. Any other error is a failure of the reply.
func replyError(ctx context.Context, scope string, mock *Mock, err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}

	return fail(ctx, vlatte.FailureReply, codes.Internal, "%s: %s: reply: %v", scope, mock, err)
}

// hasStatus reports whether err carries a gRPC status: the mock's answer, not a failure.
func hasStatus(err error) bool {
	_, ok := status.FromError(err)
	return ok
}

// closeAll closes cs, all of them, and returns their errors joined.
func closeAll(cs []io.Closer) error {
	var errs []error
	for _, c := range cs {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
