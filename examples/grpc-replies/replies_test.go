package replies_test

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

func TestFileMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	client := pb.NewTrackingClient(m.MustDial())
	requireRetrySequence(t, client)
	first := depotChoices(t, client)
	require.NoError(t, m.Reload())
	require.Equal(t, first, depotChoices(t, client))
}

func TestProgrammaticMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().SkipDefaults().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Field("id", match.Equal("retry")).
			Reply(mockgrpc.Seq(
				mockgrpc.Status(codes.Unavailable, "retry"),
				mockgrpc.Message(&pb.ParcelStatus{State: "ready"}),
			).OnSequenceEnded(mockgrpc.Message(&pb.ParcelStatus{State: "cached"}))),
		mockgrpc.Method(pb.Tracking_Dispatch_FullMethodName).Reply(mockgrpc.Echo()),
	)

	seeded := mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("id", match.Equal("random")).
		Reply(mockgrpc.RandWithSeed(42,
			mockgrpc.Message(&pb.ParcelStatus{State: "north"}),
			mockgrpc.Message(&pb.ParcelStatus{State: "south"}),
		))
	scope := m.MustMock(seeded)

	client := pb.NewTrackingClient(m.MustDial())
	requireRetrySequence(t, client)
	first := depotChoices(t, client)
	_, err := m.Remock(t.Context(), scope, seeded)
	require.NoError(t, err)
	require.Equal(t, first, depotChoices(t, client))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := client.Dispatch(ctx)
	require.NoError(t, err)

	for _, text := range []string{"a", "b"} {
		require.NoError(t, stream.Send(&pb.DispatchMessage{Text: text}))
		reply, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, text, reply.GetText())
	}

	require.NoError(t, stream.CloseSend())
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func requireRetrySequence(t *testing.T, client pb.TrackingClient) {
	t.Helper()

	ref := &pb.ParcelRef{Id: "retry"}
	_, err := client.GetParcel(t.Context(), ref)
	failure := status.Convert(err)
	require.Equal(t, codes.Unavailable, failure.Code())
	require.Equal(t, "retry", failure.Message())

	for _, want := range []string{"ready", "cached", "cached"} {
		reply, err := client.GetParcel(t.Context(), ref)
		require.NoError(t, err)
		require.Equal(t, want, reply.GetState())
	}
}

func depotChoices(t *testing.T, client pb.TrackingClient) []string {
	t.Helper()

	ref := &pb.ParcelRef{Id: "random"}
	depots := make([]string, 10)
	for i := range depots {
		reply, err := client.GetParcel(t.Context(), ref)
		require.NoError(t, err)
		depots[i] = reply.GetState()
	}

	require.Subset(t, []string{"north", "south"}, depots)
	require.Contains(t, depots, "north")
	require.Contains(t, depots, "south")
	return depots
}
