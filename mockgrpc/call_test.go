package mockgrpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCall_RecvIsForBidiCallsOnly(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Func(func(_ context.Context, call *Call) error {
		_, err := call.Recv()
		if !errors.Is(err, errNoRecv) {
			return status.Errorf(codes.Unknown, "recv: %v", err)
		}

		return nil
	})))

	_, code := greetWith(t, client(t, m), "hi")
	require.Equal(t, codes.OK, code)
}
