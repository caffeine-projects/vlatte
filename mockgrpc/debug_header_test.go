package mockgrpc

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// streamHeader opens the List stream, reads it whole, and returns its header.
func streamHeader(t *testing.T, c pb.TestServiceClient) metadata.MD {
	stream, err := c.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)

	header, err := stream.Header()
	require.NoError(t, err)

	for {
		if _, err := stream.Recv(); err != nil {
			require.True(t, errors.Is(err, io.EOF), err)
			return header
		}
	}
}

func TestDebugHeader_NamesTheMockThatMatched(t *testing.T) {
	// A client sees which mock answered its call, and how often that mock did, without the server's logs.
	m := serve(t)
	unary := m.MustMock(UnaryMethod("Greetings").Name("greet").Reply(Unary().Header("k1", "v1").Message(&pb.HiRes{Message: "bye"})))
	stream := m.MustMock(ServerStreamMethod("List").Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})))
	c := client(t, m)

	for _, hits := range []string{"1", "2"} {
		var header metadata.MD
		_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))
		require.NoError(t, err)
		require.Equal(t, unary.IDs(), header.Get(vlatte.MatchedMockIDHeader))
		require.Equal(t, []string{"greet"}, header.Get(vlatte.MatchedMockNameHeader))
		require.Equal(t, []string{hits}, header.Get(vlatte.MatchedMockHitsHeader), "the hits count this call")
		require.Equal(t, []string{"v1"}, header.Get("k1"), "next to the mock's own header")
	}

	header := streamHeader(t, c)
	require.Equal(t, stream.IDs(), header.Get(vlatte.MatchedMockIDHeader))
	require.Equal(t, []string{"1"}, header.Get(vlatte.MatchedMockHitsHeader), "each mock counts its own")
	require.Empty(t, header.Get(vlatte.MatchedMockNameHeader), "a mock with no name sends none")
}

func TestDebugHeader_AMockThatAnswersWithAStatus(t *testing.T) {
	// A status is the mock's answer, so it names its mock as a message does.
	m := serve(t)
	scope := m.MustMock(UnaryMethod("Greetings").Reply(Unary().Status(status.New(codes.NotFound, "no such user"))))

	var header metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, scope.IDs(), header.Get(vlatte.MatchedMockIDHeader))
}

func TestDebugHeader_NoneWhenNoMockMatched(t *testing.T) {
	// No mock answered, so none is named: the failure trailer says the server answered.
	m := serve(t)

	var header, trailer metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, header.Get(vlatte.MatchedMockIDHeader))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.FailureHeader))
}

func TestDebugHeader_NoDebugHeadersTurnsThemOff(t *testing.T) {
	// The switch is all or nothing, unary and stream alike, such as for a client that compares whole headers.
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).NoDebugHeaders())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		UnaryMethod("Greetings").Name("greet").Reply(Unary().Message(&pb.HiRes{Message: "bye"})),
		ServerStreamMethod("List").Name("list").Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})),
	)
	c := client(t, m)

	var header metadata.MD
	_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))
	require.NoError(t, err)

	for _, md := range []metadata.MD{header, streamHeader(t, c)} {
		for _, key := range []string{vlatte.MatchedMockIDHeader, vlatte.MatchedMockNameHeader, vlatte.MatchedMockHitsHeader} {
			require.Empty(t, md.Get(key), key)
		}
	}
}
