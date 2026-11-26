package verification_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().Journal("").
		MockFiles("testdata/lookup.mock.json")
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	client := pb.NewTrackingClient(m.MustDial())
	request := &pb.ParcelRef{Id: "P1"}
	reply, err := client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "in_transit", reply.GetState())

	reply, err = client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "in_transit", reply.GetState())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "P2"})
	require.Equal(t, codes.NotFound, status.Code(err))

	requests := m.VerifyRequests()
	p1, err := requests.Matching(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("id", match.Equal("P1"))).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, p1)

	// The journal keeps calls even when no mock matches.
	p2, err := requests.Matching(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("id", match.Equal("P2"))).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, p2)
}

func TestProgrammaticMocks(t *testing.T) {
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().Journal("")
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		ID("lookup").
		Field("id", match.Equal("P1")).
		Reply(mockgrpc.Message(&pb.ParcelStatus{State: "in_transit"})))

	client := pb.NewTrackingClient(m.MustDial())
	request := &pb.ParcelRef{Id: "P1"}
	reply, err := client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "in_transit", reply.GetState())

	reply, err = client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "in_transit", reply.GetState())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "P2"})
	require.Equal(t, codes.NotFound, status.Code(err))

	requests := m.VerifyRequests()
	p1, err := requests.Matching(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("id", match.Equal("P1"))).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, p1)

	// The journal keeps calls even when no mock matches.
	p2, err := requests.Matching(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("id", match.Equal("P2"))).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, p2)
}
