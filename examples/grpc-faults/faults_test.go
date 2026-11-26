package faults_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().InMemory().MockFiles("testdata/delay.mock.yaml"))
	m.MustStart()
	t.Cleanup(m.Close)

	requireCanceledWatch(t, pb.NewTrackingClient(m.MustDial()))
}

func TestProgrammaticMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Addr("127.0.0.1:0"))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Priority(10).
			Reply(mockgrpc.Message(&pb.ParcelStatus{State: "accepted"}).
				Header("x-feed", "open").
				MessageDelay(vlatte.Latency(time.Hour))),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Field("id", match.Equal("abort")).
			Reply(cutAfterUpdate(vlatte.ErrAbort)),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Field("id", match.Equal("reset")).
			Reply(cutAfterUpdate(vlatte.ErrReset)),
	)

	requireCanceledWatch(t, pb.NewTrackingClient(m.MustDial()))

	// Each cut needs its own connection.
	abortClient := pb.NewTrackingClient(m.MustDial())
	requireCutWatch(t, abortClient, "abort")

	resetClient := pb.NewTrackingClient(m.MustDial())
	requireCutWatch(t, resetClient, "reset")
}

func requireCanceledWatch(t *testing.T, client pb.TrackingClient) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := client.WatchParcel(ctx, &pb.ParcelRef{Id: "P1"})
	require.NoError(t, err)

	header, err := stream.Header()
	require.NoError(t, err)
	require.Equal(t, []string{"open"}, header.Get("x-feed"))

	cancel()
	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
}

func cutAfterUpdate(cause error) mockgrpc.Reply {
	return mockgrpc.Func(func(ctx context.Context, call *mockgrpc.Call) error {
		if err := grpc.SetTrailer(ctx, metadata.Pairs("x-resume", "1")); err != nil {
			return err
		}
		if err := call.Send(&pb.ParcelStatus{State: "accepted"}); err != nil {
			return err
		}

		return cause
	})
}

func requireCutWatch(t *testing.T, client pb.TrackingClient, id string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := client.WatchParcel(ctx, &pb.ParcelRef{Id: id})
	require.NoError(t, err)

	update, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "accepted", update.GetState())

	_, err = stream.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Empty(t, stream.Trailer().Get("x-resume"))
}
