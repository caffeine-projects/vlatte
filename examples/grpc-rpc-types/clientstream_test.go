package rpctypes_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestUploadScansFileMocks(t *testing.T) {
	setup := mockgrpc.Setup().Silent().InMemory().
		MockFiles("testdata/clientstream.mock.yaml")
	server := mockgrpc.MustNew(setup)
	t.Cleanup(server.Close)
	server.MustStart()

	client := pb.NewTrackingClient(server.MustDial())
	stream, err := client.UploadScans(t.Context())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.Scan{Site: "north"}))
	require.NoError(t, stream.Send(&pb.Scan{Site: "south"}))
	receipt, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.Equal(t, uint32(2), receipt.GetAccepted())

	checkRejectedUploads(t, client)
}

func TestUploadScansProgrammaticMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	t.Cleanup(server.Close)
	server.MustStart()

	server.MustMock(
		mockgrpc.Method(pb.Tracking_UploadScans_FullMethodName).
			Messages(&pb.Scan{Site: "north"}, &pb.Scan{Site: "south"}).
			Reply(mockgrpc.Template(`{"accepted":{{ len .Request.Message }}}`)),
		mockgrpc.Method(pb.Tracking_UploadScans_FullMethodName).
			Priority(10).
			Reply(mockgrpc.Status(codes.InvalidArgument, "expected north then south")),
	)

	client := pb.NewTrackingClient(server.MustDial())
	stream, err := client.UploadScans(t.Context())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.Scan{Site: "north"}))
	require.NoError(t, stream.Send(&pb.Scan{Site: "south"}))
	receipt, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.Equal(t, uint32(2), receipt.GetAccepted())

	checkRejectedUploads(t, client)
}

func checkRejectedUploads(t *testing.T, client pb.TrackingClient) {
	t.Helper()

	reversed, err := client.UploadScans(t.Context())
	require.NoError(t, err)
	require.NoError(t, reversed.Send(&pb.Scan{Site: "south"}))
	require.NoError(t, reversed.Send(&pb.Scan{Site: "north"}))
	_, err = reversed.CloseAndRecv()
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "expected north then south", status.Convert(err).Message())

	empty, err := client.UploadScans(t.Context())
	require.NoError(t, err)
	_, err = empty.CloseAndRecv()
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "expected north then south", status.Convert(err).Message())
}
