package xgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	pb "github.com/caffeine-projects/vlatte/flavor/xgrpc/internal/protobuf"
	. "github.com/caffeine-projects/vlatte/filter"
)

func TestUnary(t *testing.T) {
	m := New()
	defer m.Close()
	pb.RegisterTestServiceServer(m.Server(), pb.UnimplementedTestServiceServer{})
	m.MustStart()
	m.MustMock(UnaryMethod("Greetings").
		Header("h1", Eq("v1")).
		Field("message", Eq("hi")).
		Reply(Unary().
			Header("k1", "v1", "v2").
			Trailer("tk1", "tv1", "tv2").
			Message(&pb.HiRes{Message: "bye"})))

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "h1", "v1")
	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

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
