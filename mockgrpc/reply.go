package mockgrpc

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
)

var (
	errNoReply   = errors.New("mock: no reply set: use Reply")
	errNoReplier = errors.New("reply: the factory made no replier")
)

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

func interceptError(format string, a ...any) error {
	return status.Error(codes.Internal, fmt.Errorf(format, a...).Error())
}
