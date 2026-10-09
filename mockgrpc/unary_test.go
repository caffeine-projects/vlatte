package mockgrpc

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestUnary(t *testing.T) {
	m := MustNew()
	defer m.Close()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
	m.MustStart()
	m.MustMock(UnaryMethod("Greetings").
		Header("h1", match.Eq("v1")).
		Field("message", match.Eq("hi")).
		Reply(Unary().
			Header("k1", "v1", "v2").
			Trailer("tk1", "tv1", "tv2").
			Message(&pb.HiRes{Message: "bye"})))

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "h1", "v1")
	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)
	header := make(metadata.MD)
	trailer := make(metadata.MD)
	res, err := client.Greetings(ctx, &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))

	require.NoError(t, err)
	require.Equal(t, 2, header.Len()) // includes the content-type
	require.Equal(t, []string{"v1", "v2"}, header.Get("k1"))
	require.Equal(t, 1, trailer.Len())
	require.Equal(t, []string{"tv1", "tv2"}, trailer.Get("tk1"))
	require.Equal(t, "bye", res.Message)
}

func TestUnaryReply_ChangesAfterMockReachOnlyLaterMocks(t *testing.T) {
	// #27: a mock served the reply it was given, so a header added to the reply afterwards went out on the calls of
	// that mock, and raced with them. Each mock gets a replier of its own now: the change reaches only the mocks built
	// afterwards.
	m := MustNew()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
	m.MustStart()
	t.Cleanup(m.Close)

	reply := Unary().Header("x-a", "1").Message(&pb.HiRes{Message: "hi"})
	b := UnaryMethod("Greetings").Reply(reply)
	scope := m.MustMock(b)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)
	greet := func() metadata.MD {
		header := make(metadata.MD)
		_, err := client.Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))
		require.NoError(t, err)

		return header
	}

	// Changed after Mock, and again while the mock serves.
	reply.Header("x-b", "2")

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 10 {
			reply.Header("x-b", "2")
		}
	})

	for range 10 {
		header := greet()
		require.Equal(t, []string{"1"}, header.Get("x-a"))
		require.Empty(t, header.Get("x-b"), "the served mock has no x-b")
	}

	wg.Wait()

	_, err = m.Remock(t.Context(), scope, b)
	require.NoError(t, err)
	require.NotEmpty(t, greet().Get("x-b"), "a mock built afterwards has it")
}

func TestUnaryReply_Status(t *testing.T) {
	// A reply with a status fails the call with that status, after sending the header.
	m := MustNew()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(UnaryMethod("Greetings").
		Reply(Unary().Header("k", "v").Status(status.New(codes.PermissionDenied, "denied"))))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	header := make(metadata.MD)
	_, err = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))

	st, ok := status.FromError(err)
	require.True(t, ok, "a gRPC status: %v", err)
	require.Equal(t, codes.PermissionDenied, st.Code())
	require.Equal(t, "denied", st.Message())
	require.Equal(t, []string{"v"}, header.Get("k"))
}
