package mockgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
)

// UnaryInterceptor serves the unary calls of the services registered on the server, whose handlers grpc runs it in:
// grpc decoded the request message already.
func (in *interceptors) UnaryInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	_ grpc.UnaryHandler,
) (any, error) {
	// The services the server implements itself, such as reflection, have no unary methods.
	md, ok := in.app.resolver.method(info.FullMethod)
	if !ok {
		return nil, in.unknown(ctx, info.FullMethod)
	}

	msg, ok := req.(proto.Message)
	if !ok {
		return nil, fail(ctx, vlatte.FailureMatch, codes.Internal, "grpc: the request of %s is a %T, not a protobuf "+
			"message", info.FullMethod, req)
	}

	call := newCall(in.app, info.FullMethod, md, incoming(ctx), nil)
	call.message = msg

	if err := in.serve(ctx, call); err != nil {
		return nil, err
	}

	return call.answered(), nil
}
