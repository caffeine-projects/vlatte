package recording_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	upstream := mockgrpc.MustNew(mockgrpc.Setup().Silent().Addr("127.0.0.1:0").
		MockFiles("testdata/upstream.mock.yaml"))
	t.Cleanup(upstream.Close)
	upstream.MustStart()

	recorder := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	t.Cleanup(recorder.Close)
	recorder.MustStart()

	dir := t.TempDir()
	require.NoError(t, recorder.StartRecording(
		mockgrpc.RecordTarget(upstream.Addr()),
		mockgrpc.RecordDir(dir),
		mockgrpc.RecordFormat("yaml"),
		mockgrpc.RecordRequestHeaders("x-tenant"),
	))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	demo := metadata.AppendToOutgoingContext(ctx, "x-tenant", "demo")
	recorded := pb.NewTrackingClient(recorder.MustDial())
	ref := &pb.ParcelRef{Id: "P1"}
	checkParcelState(t, demo, recorded, ref, "accepted")
	checkParcelState(t, demo, recorded, ref, "delivered")
	checkWatch(t, demo, recorded)

	result, err := recorder.StopRecording(ctx)
	require.NoError(t, err)
	require.Len(t, result.Recordings, 2)
	glob := filepath.Join(dir, "*.mock.yaml")
	files, err := filepath.Glob(glob)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{result.Recordings[0].File, result.Recordings[1].File}, files)

	upstream.Close()
	recorder.Close()

	replay := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().MockFiles(glob))
	t.Cleanup(replay.Close)
	replay.MustStart()
	played := pb.NewTrackingClient(replay.MustDial())

	checkParcelState(t, demo, played, ref, "accepted")
	checkParcelState(t, demo, played, ref, "delivered")
	checkParcelState(t, demo, played, ref, "delivered")
	checkWatch(t, demo, played)

	otherTenant := metadata.AppendToOutgoingContext(ctx, "x-tenant", "other")
	var trailer metadata.MD
	_, err = played.GetParcel(otherTenant, ref, grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
}

func TestProgrammaticMocks(t *testing.T) {
	upstream := mockgrpc.MustNew(mockgrpc.Setup().Silent().Addr("127.0.0.1:0"))
	t.Cleanup(upstream.Close)
	upstream.MustStart()
	upstream.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P1"}).
			Reply(mockgrpc.Seq(
				mockgrpc.Message(&pb.ParcelStatus{State: "accepted"}),
				mockgrpc.Message(&pb.ParcelStatus{State: "delivered"}),
			)),
		mockgrpc.Method(pb.Tracking_WatchParcel_FullMethodName).
			Message(&pb.ParcelRef{Id: "P2"}).
			Reply(mockgrpc.Messages(&pb.ParcelStatus{State: "accepted"}).
				Status(codes.Unavailable, "feed offline").Trailer("x-resume", "1")),
	)

	recorder := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	t.Cleanup(recorder.Close)
	recorder.MustStart()

	dir := t.TempDir()
	require.NoError(t, recorder.StartRecording(
		mockgrpc.RecordTarget(upstream.Addr()),
		mockgrpc.RecordDir(dir),
		mockgrpc.RecordFormat("yaml"),
		mockgrpc.RecordRequestHeaders("x-tenant"),
	))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	demo := metadata.AppendToOutgoingContext(ctx, "x-tenant", "demo")
	recorded := pb.NewTrackingClient(recorder.MustDial())
	ref := &pb.ParcelRef{Id: "P1"}
	checkParcelState(t, demo, recorded, ref, "accepted")
	checkParcelState(t, demo, recorded, ref, "delivered")
	checkWatch(t, demo, recorded)

	result, err := recorder.StopRecording(ctx)
	require.NoError(t, err)
	require.Len(t, result.Recordings, 2)
	glob := filepath.Join(dir, "*.mock.yaml")
	files, err := filepath.Glob(glob)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{result.Recordings[0].File, result.Recordings[1].File}, files)

	upstream.Close()
	recorder.Close()

	replay := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().MockFiles(glob))
	t.Cleanup(replay.Close)
	replay.MustStart()
	played := pb.NewTrackingClient(replay.MustDial())

	checkParcelState(t, demo, played, ref, "accepted")
	checkParcelState(t, demo, played, ref, "delivered")
	checkParcelState(t, demo, played, ref, "delivered")
	checkWatch(t, demo, played)

	otherTenant := metadata.AppendToOutgoingContext(ctx, "x-tenant", "other")
	var trailer metadata.MD
	_, err = played.GetParcel(otherTenant, ref, grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
}

func checkParcelState(t *testing.T, ctx context.Context, client pb.TrackingClient, ref *pb.ParcelRef, want string) {
	t.Helper()

	res, err := client.GetParcel(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, want, res.GetState())
}

func checkWatch(t *testing.T, ctx context.Context, client pb.TrackingClient) {
	t.Helper()

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
