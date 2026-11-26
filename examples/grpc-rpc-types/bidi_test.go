package rpctypes_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestDispatchFileMocks(t *testing.T) {
	setup := mockgrpc.Setup().Silent().InMemory().
		MockFiles("testdata/bidi.mock.yaml")
	server := mockgrpc.MustNew(setup)
	t.Cleanup(server.Close)
	server.MustStart()

	client := pb.NewTrackingClient(server.MustDial())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := client.Dispatch(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.DispatchMessage{Text: "route"}))
	requireDispatchText(t, stream, "north")
	requireDispatchText(t, stream, "south")

	require.NoError(t, stream.Send(&pb.DispatchMessage{Text: "?"}))
	requireDispatchText(t, stream, "unknown")

	require.NoError(t, stream.CloseSend())
	requireDispatchText(t, stream, "bye")
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, []string{"complete"}, stream.Trailer().Get("x-dispatch"))

	checkDispatchStop(t, ctx, client)
}

func TestDispatchProgrammaticMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	t.Cleanup(server.Close)
	server.MustStart()

	server.MustMock(mockgrpc.Method(pb.Tracking_Dispatch_FullMethodName).Reply(mockgrpc.Bidi().
		On(match.Field("text", match.Equal("route")), mockgrpc.Messages(
			&pb.DispatchMessage{Text: "north"},
			&pb.DispatchMessage{Text: "south"},
		)).
		On(match.Field("text", match.Equal("stop")), mockgrpc.Status(codes.FailedPrecondition, "dispatch closed")).
		Otherwise(mockgrpc.Message(&pb.DispatchMessage{Text: "unknown"})).
		OnEnd(mockgrpc.Message(&pb.DispatchMessage{Text: "bye"})).
		Trailer("x-dispatch", "complete")))

	client := pb.NewTrackingClient(server.MustDial())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := client.Dispatch(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.DispatchMessage{Text: "route"}))
	requireDispatchText(t, stream, "north")
	requireDispatchText(t, stream, "south")

	require.NoError(t, stream.Send(&pb.DispatchMessage{Text: "?"}))
	requireDispatchText(t, stream, "unknown")

	require.NoError(t, stream.CloseSend())
	requireDispatchText(t, stream, "bye")
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, []string{"complete"}, stream.Trailer().Get("x-dispatch"))

	checkDispatchStop(t, ctx, client)
}

func requireDispatchText(t *testing.T, stream pb.Tracking_DispatchClient, want string) {
	t.Helper()

	reply, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, want, reply.GetText())
}

func checkDispatchStop(t *testing.T, ctx context.Context, client pb.TrackingClient) {
	t.Helper()

	stopped, err := client.Dispatch(ctx)
	require.NoError(t, err)
	require.NoError(t, stopped.Send(&pb.DispatchMessage{Text: "stop"}))
	_, err = stopped.Recv()
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, "dispatch closed", status.Convert(err).Message())
}
