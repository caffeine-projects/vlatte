package mockgrpc

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
)

var (
	errNoReply   = errors.New("mock: no reply set: use Reply")
	errNoReplier = errors.New("reply: the factory made no replier")
)

// _failureKey is vlatte.FailureHeader as a metadata key, which gRPC wants lowercase.
var _failureKey = strings.ToLower(vlatte.FailureHeader)

// newReplier asks f for the replier of a mock of app. A missing reply, or a factory that makes no replier, fails the
// build: before, the mock was added and failed every call.
func newReplier[I any](f vlatte.ReplierFactory[I, any, *Server], app *Server) (vlatte.Replier[I, any], error) {
	if f == nil {
		return nil, errNoReply
	}

	r, err := f.NewReplier(app)
	if err != nil {
		return nil, err
	}

	if r == nil {
		return nil, errNoReplier
	}

	return r, nil
}

// fail answers a call the server failed to serve, or no mock matched: it adds failure, a vlatte.Failure* value, to
// the trailer of the call, so a client tells the answer from a mock's, and returns a status error with code c.
func fail(ctx context.Context, failure string, c codes.Code, format string, a ...any) error {
	_ = grpc.SetTrailer(ctx, metadata.Pairs(_failureKey, failure))
	return status.Errorf(c, format, a...)
}

// debugHeader is the header metadata naming mock, the mock that matched a call: its ID, its hits, this call included,
// and its name, when it has one. Inc counts the call once it is answered.
func debugHeader(mock *Mock) metadata.MD {
	md := metadata.Pairs(
		vlatte.MatchedMockIDHeader, mock.ID(),
		vlatte.MatchedMockHitsHeader, strconv.FormatInt(mock.Hits()+1, 10),
	)

	if name := mock.Name(); name != "" {
		md.Set(vlatte.MatchedMockNameHeader, name)
	}

	return md
}

// replyError is the answer to a call whose mock replied err. An error with a gRPC status is the mock's answer, such
// as a status set with UnaryReply.Status, so it is returned as is. Any other error is a failure of the reply.
func replyError(ctx context.Context, scope string, mock *Mock, err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}

	return fail(ctx, vlatte.FailureReply, codes.Internal, "%s: %s: reply: %v", scope, mock, err)
}
