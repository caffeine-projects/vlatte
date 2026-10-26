package mockgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestDescribe_AMockBuiltInGoLoadsBackTheSame(t *testing.T) {
	// The admin API returns and saves mocks as their descriptions: one built in Go, of what mock files hold, loads
	// back as a mock that answers the same.
	builders := []vlatte.Builder[*Mock, *Server]{
		Method("Greetings").ID("a").Header("x-team", match.Equal("core")).JSON(`{"message": "a"}`).
			Reply(Message(&pb.HiRes{Message: "for a"}).Header("h", "1")),
		Method("Greetings").ID("b").Priority(1).Message(&pb.HiReq{Message: "b"}).
			Reply(Status(codes.NotFound, "no b").Details(&errdetails.ErrorInfo{Reason: "B"})),
		Method("List").ID("c").Reply(JSON(`{"key": "x"}`, `{"key": "y"}`).Status(codes.Aborted, "cut")),
	}

	origin := fileServer(t, Setup())
	scope := origin.MustMock(builders...)

	loaded := fileServer(t, Setup())

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)

		_, err = loaded.Mock(FromBytes(doc, "json"))
		require.NoError(t, err, "%s", doc)
	}

	for _, m := range []*Server{origin, loaded} {
		c := pb.NewTestServiceClient(m.MustDial())

		ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
		res, err := c.Greetings(ctx, &pb.HiReq{Message: "a"})
		require.NoError(t, err)
		require.Equal(t, "for a", res.GetMessage())

		_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "b"})
		st := status.Convert(err)
		require.Equal(t, codes.NotFound, st.Code())
		require.Len(t, st.Details(), 1)

		stream, err := c.List(context.WithoutCancel(t.Context()), &pb.ListReq{})
		require.NoError(t, err)

		item, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "x", item.GetKey())
	}
}
