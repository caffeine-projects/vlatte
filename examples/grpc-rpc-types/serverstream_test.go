package rpctypes_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestWatchParcelFileMocks(t *testing.T) {
	setup := mockgrpc.Setup().ConfigSources().Silent().InMemory().
		MockFiles("testdata/serverstream.mock.yaml")
	server := mockgrpc.MustNew(setup)
	t.Cleanup(server.Close)
	server.MustStart()

	client := pb.NewTrackingClient(server.MustDial())
	stream, err := client.WatchParcel(t.Context(), &pb.ParcelRef{Id: "P1"})
	require.NoError(t, err)
	states, err := receiveStates(stream)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, []string{"accepted", "in_transit", "delivered"}, states)

	checkEmptyAndInterruptedWatches(t, client)
}

func TestWatchParcelProgrammaticMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory())
	t.Cleanup(server.Close)
	server.MustStart()

	server.MustMock(
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P1"}).
			Reply(mockgrpc.Messages(
				&pb.ParcelStatus{State: "accepted"},
				&pb.ParcelStatus{State: "in_transit"},
				&pb.ParcelStatus{State: "delivered"},
			)),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P0"}).
			Reply(mockgrpc.Messages()),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P2"}).
			Reply(mockgrpc.Message(&pb.ParcelStatus{State: "accepted"}).
				Status(codes.Unavailable, "feed offline").
				Trailer("x-resume", "1")),
	)

	client := pb.NewTrackingClient(server.MustDial())
	stream, err := client.WatchParcel(t.Context(), &pb.ParcelRef{Id: "P1"})
	require.NoError(t, err)
	states, err := receiveStates(stream)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, []string{"accepted", "in_transit", "delivered"}, states)

	checkEmptyAndInterruptedWatches(t, client)
}

func receiveStates(stream pb.Tracking_WatchParcelClient) ([]string, error) {
	var states []string
	for {
		update, err := stream.Recv()
		if err != nil {
			return states, err
		}
		states = append(states, update.GetState())
	}
}

func checkEmptyAndInterruptedWatches(t *testing.T, client pb.TrackingClient) {
	t.Helper()

	empty, err := client.WatchParcel(t.Context(), &pb.ParcelRef{Id: "P0"})
	require.NoError(t, err)
	_, err = empty.Recv()
	require.ErrorIs(t, err, io.EOF)

	partial, err := client.WatchParcel(t.Context(), &pb.ParcelRef{Id: "P2"})
	require.NoError(t, err)
	states, err := receiveStates(partial)
	require.Equal(t, []string{"accepted"}, states)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, "feed offline", status.Convert(err).Message())
	require.Equal(t, []string{"1"}, partial.Trailer().Get("x-resume"))
}
