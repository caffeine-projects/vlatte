package mockgrpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
)

// StreamInterceptor serves every streaming call, and the calls of the services the server has no implementation of,
// unary ones included: grpc gives those to its unknown service handler, as bidi streams. The method's descriptor
// tells what the call is.
func (in *interceptors) StreamInterceptor(
	_ any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	_ grpc.StreamHandler,
) error {
	ctx := stream.Context()

	md, ok := in.app.resolver.method(info.FullMethod)
	if !ok {
		return in.unknown(ctx, info.FullMethod)
	}

	call := newCall(in.app, info.FullMethod, md, incoming(ctx), stream)

	if err := call.receive(); err != nil {
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}

		return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: reading the request: %v", typeOf(md), err)
	}

	if err := in.serve(ctx, call); err != nil {
		return err
	}

	// A call that answers one message sends it once the reply returned.
	if !md.IsStreamingServer() {
		return stream.SendMsg(call.answered())
	}

	return nil
}
