package mockgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestPrep(t *testing.T) {
	m := MustNew()
	defer m.Close()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})

	err := m.Prep(t.Context(), vlatte.RecipeFunc[*Mock, *Server](
		func(_ context.Context, in *vlatte.RecipeInput[*Mock, *Server]) error {
			_, err := in.Flavor.Mock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "from recipe"})))
			return err
		}))
	require.NoError(t, err)
	m.MustStart()

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// The mock a recipe adds serves calls like any other mock.
	res, err := pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)
	require.Equal(t, "from recipe", res.Message)
}
