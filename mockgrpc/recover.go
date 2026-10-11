package mockgrpc

import (
	"context"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte"
)

// recoverUnary answers a panic while serving a unary call with Internal and the panic failure, instead of letting it
// crash the server.
func (in *interceptors) recoverUnary(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	next grpc.UnaryHandler,
) (res any, err error) {
	defer func() {
		if v := recover(); v != nil {
			res, err = nil, in.panicked(ctx, info.FullMethod, v)
		}
	}()

	return next(ctx, req)
}

// recoverStream is recoverUnary for streaming calls.
func (in *interceptors) recoverStream(
	srv any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	next grpc.StreamHandler,
) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = in.panicked(stream.Context(), info.FullMethod, v)
		}
	}()

	return next(srv, stream)
}

// panicked logs the panic v of a call to method, with its stack, and returns the answer to the call.
func (in *interceptors) panicked(ctx context.Context, method string, v any) error {
	in.app.logger.Error().Msgf("grpc: panic serving %s: %v\n%s", method, v, debug.Stack())
	return fail(ctx, vlatte.FailurePanic, codes.Internal, "panic: %v", v)
}
