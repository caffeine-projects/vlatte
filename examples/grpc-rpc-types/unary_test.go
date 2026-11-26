package rpctypes_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestGetParcelFileMocks(t *testing.T) {
	setup := mockgrpc.Setup().ConfigSources().Silent().InMemory().
		MockFiles("testdata/unary.mock.yaml")
	server := mockgrpc.MustNew(setup)
	t.Cleanup(server.Close)
	server.MustStart()

	client := pb.NewTrackingClient(server.MustDial())
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-tenant", "demo")
	var header, trailer metadata.MD
	parcel, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "P1"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)
	require.Equal(t, "in_transit", parcel.GetState())
	require.Equal(t, []string{"north"}, header.Get("x-depot"))
	require.Equal(t, []string{"tracker"}, trailer.Get("x-source"))

	checkMissingParcels(t, client)
}

func TestGetParcelProgrammaticMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory())
	t.Cleanup(server.Close)
	server.MustStart()

	server.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Header("x-tenant", match.Equal("demo")).
			Field("id", match.Equal("P1")).
			Reply(mockgrpc.Message(&pb.ParcelStatus{State: "in_transit"}).
				Header("x-depot", "north").
				Trailer("x-source", "tracker")),
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			Header("x-tenant", match.Equal("demo")).
			Field("id", match.Equal("P404")).
			Reply(mockgrpc.Status(codes.NotFound, "parcel missing").Details(
				&errdetails.ResourceInfo{ResourceType: "parcel", ResourceName: "P404"},
			)),
	)

	client := pb.NewTrackingClient(server.MustDial())
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-tenant", "demo")
	var header, trailer metadata.MD
	parcel, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "P1"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)
	require.Equal(t, "in_transit", parcel.GetState())
	require.Equal(t, []string{"north"}, header.Get("x-depot"))
	require.Equal(t, []string{"tracker"}, trailer.Get("x-source"))

	checkMissingParcels(t, client)
}

func checkMissingParcels(t *testing.T, client pb.TrackingClient) {
	t.Helper()

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-tenant", "demo")
	_, err := client.GetParcel(ctx, &pb.ParcelRef{Id: "P404"})
	missing := status.Convert(err)
	require.Equal(t, codes.NotFound, missing.Code())
	require.Equal(t, "parcel missing", missing.Message())
	details := missing.Details()
	require.Len(t, details, 1)
	info, ok := details[0].(*errdetails.ResourceInfo)
	require.True(t, ok)
	require.Equal(t, "parcel", info.GetResourceType())
	require.Equal(t, "P404", info.GetResourceName())

	// Omitting the tenant misses both mocks. VLatte identifies the miss in a trailer.
	var miss metadata.MD
	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "P1"}, grpc.Trailer(&miss))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, miss.Get(vlatte.HeaderFailure))
}
