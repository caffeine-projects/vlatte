package mockgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// A method name matched anywhere in the full method, so a mock for Greet answered Greetings, and one for Lis answered
// List. A name matches whole segments now: the full method, or its end after a "/" or a ".", so the method, its service
// and its package are written whole. Unary and stream mocks read it the same way.

func TestMethod_UnaryMatchesWholeNames(t *testing.T) {
	tests := []struct {
		give string
		want codes.Code
	}{
		{"Greetings", codes.OK},
		{"TestService/Greetings", codes.OK},
		{"protobuf.TestService/Greetings", codes.OK},
		{"/protobuf.TestService/Greetings", codes.OK},
		{"Greet", codes.NotFound},
		{"etings", codes.NotFound},
		{"Service/Greetings", codes.NotFound},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			m, client := methodServer(t)
			m.MustMock(UnaryMethod(tt.give).Reply(Unary().Message(&pb.HiRes{Message: "bye"})))

			_, err := client.Greetings(context.Background(), &pb.HiReq{Message: "hi"})
			require.Equal(t, tt.want, status.Code(err))
		})
	}
}

func TestMethod_StreamMatchesWholeNames(t *testing.T) {
	tests := []struct {
		give string
		want codes.Code
	}{
		{"List", codes.OK},
		{"TestService/List", codes.OK},
		{"Lis", codes.NotFound},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			m, client := methodServer(t)
			m.MustMock(ServerStreamMethod(tt.give).Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})))

			stream, err := client.List(context.Background(), &pb.ListReq{})
			require.NoError(t, err)

			_, err = stream.Recv()
			require.Equal(t, tt.want, status.Code(err))
		})
	}
}

// methodServer starts a server of the test service, and returns it with a client of it.
func methodServer(t *testing.T) (*Server, pb.TestServiceClient) {
	t.Helper()

	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return m, pb.NewTestServiceClient(conn)
}
