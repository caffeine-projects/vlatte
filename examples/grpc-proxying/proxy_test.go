package proxying_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	upstream := startUpstream(t)

	// Let the OS pick a free upstream port so the example can run alongside other tests.
	fixture, err := os.ReadFile("testdata/proxy.mock.yaml")
	require.NoError(t, err)
	fixture = bytes.ReplaceAll(fixture, []byte("127.0.0.1:50052"), []byte(upstream.Addr()))

	path := filepath.Join(t.TempDir(), "proxy.mock.yaml")
	require.NoError(t, os.WriteFile(path, fixture, 0o600))

	front := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().MockFiles(path))
	t.Cleanup(front.Close)
	front.MustStart()

	client := pb.NewTrackingClient(front.MustDial())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-tenant", "wrong", "x-drop", "1")

	cached, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "local"})
	require.NoError(t, err)
	require.Equal(t, "cached", cached.GetState())

	var header metadata.MD
	forwarded, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "P1"}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, "forwarded", forwarded.GetState())
	require.Equal(t, []string{"upstream"}, header.Get("x-hop"))

	stream, err := client.WatchParcel(ctx, &pb.ParcelRef{Id: "P2"})
	require.NoError(t, err)
	update, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "accepted", update.GetState())

	_, err = stream.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, "feed offline", status.Convert(err).Message())
	require.Equal(t, []string{"1"}, stream.Trailer().Get("x-resume"))
}

func TestProgrammaticMocks(t *testing.T) {
	upstream := startUpstream(t)

	front := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	t.Cleanup(front.Close)
	front.MustStart()
	front.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "local"}).
			Reply(mockgrpc.Message(&pb.ParcelStatus{State: "cached"})),
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).Priority(10).
			Reply(mockgrpc.From(upstream.Addr()).Header("x-tenant", "demo").RemoveHeaders("x-drop")),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P2"}).
			Reply(mockgrpc.From(upstream.Addr())),
	)

	client := pb.NewTrackingClient(front.MustDial())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-tenant", "wrong", "x-drop", "1")

	cached, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "local"})
	require.NoError(t, err)
	require.Equal(t, "cached", cached.GetState())

	var header metadata.MD
	forwarded, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "P1"}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, "forwarded", forwarded.GetState())
	require.Equal(t, []string{"upstream"}, header.Get("x-hop"))

	stream, err := client.WatchParcel(ctx, &pb.ParcelRef{Id: "P2"})
	require.NoError(t, err)
	update, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "accepted", update.GetState())

	_, err = stream.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, "feed offline", status.Convert(err).Message())
	require.Equal(t, []string{"1"}, stream.Trailer().Get("x-resume"))
}

func startUpstream(t *testing.T) *mockgrpc.Server {
	t.Helper()

	upstream := mockgrpc.MustNew(mockgrpc.Setup().Silent().Addr("127.0.0.1:0"))
	t.Cleanup(upstream.Close)
	upstream.MustStart()
	upstream.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Header("x-tenant", match.Equal("demo")).
			Header("x-drop", match.Not(match.Present())).
			Reply(mockgrpc.Message(&pb.ParcelStatus{State: "forwarded"}).Header("x-hop", "upstream")),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P2"}).
			Reply(mockgrpc.Messages(&pb.ParcelStatus{State: "accepted"}).
				Status(codes.Unavailable, "feed offline").Trailer("x-resume", "1")),
	)
	return upstream
}
