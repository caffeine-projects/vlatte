package mockgrpc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

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
	unary := m.MustMock(Method("Greetings").Name("greet").Reply(Message(&pb.HiRes{Message: "bye"}).Header("k1", "v1")))
	stream := m.MustMock(Method("List").Reply(Messages(&pb.ListItem{Key: "a"})))
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
	scope := m.MustMock(Method("Greetings").Reply(Status(codes.NotFound, "no such user")))

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
		Method("Greetings").Name("greet").Reply(Message(&pb.HiRes{Message: "bye"})),
		Method("List").Name("list").Reply(Messages(&pb.ListItem{Key: "a"})),
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

func TestDebugHeader_ACallTheMockDoesntCountSendsNoHits(t *testing.T) {
	// The hits of the header are the mock's once the call counts: a template that renders no message fails the call
	// before its answer goes out, so the header names no mock, the mock counts nothing, and Once still matches.
	m := serve(t)
	scope := m.MustMock(Method("Greetings").Once().Reply(Template(`{"message": {{ .Request.Message.message }}}`)))
	c := client(t, m)

	var header metadata.MD
	_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "not json"}, grpc.Header(&header))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Empty(t, header.Get(vlatte.MatchedMockHitsHeader))
	require.Empty(t, header.Get(vlatte.MatchedMockIDHeader))
	require.Zero(t, scope.Hits())

	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: `"ok"`}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, "ok", res.GetMessage())
	require.Equal(t, []string{"1"}, header.Get(vlatte.MatchedMockHitsHeader))
	require.Equal(t, int64(1), scope.Hits())
}

func TestDebugHeader_AClientThatLeavesAfterTheHeaderTakesTheHitBack(t *testing.T) {
	// A stream's header goes out with its first message, which counts the call: a client that leaves before the
	// answer ends takes the hit back, so the next call's header says 1 again.
	m := serve(t)
	scope := m.MustMock(Method("List").Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).
		MessageDelayFunc(delays(0, time.Hour, 0, 0))))
	c := client(t, m)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stream, err := c.List(ctx, &pb.ListReq{})
	require.NoError(t, err)

	header, err := stream.Header()
	require.NoError(t, err)
	require.Equal(t, []string{"1"}, header.Get(vlatte.MatchedMockHitsHeader))

	_, err = stream.Recv()
	require.NoError(t, err)

	cancel()

	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
	require.Eventually(t, func() bool { return scope.Hits() == 0 }, time.Second, time.Millisecond)

	require.Equal(t, []string{"1"}, streamHeader(t, c).Get(vlatte.MatchedMockHitsHeader))
	require.Equal(t, int64(1), scope.Hits())
}

func TestDebugHeader_AClientThatLeavesDuringTheDelayIsNotCounted(t *testing.T) {
	m := serve(t)
	scope := m.MustMock(Method("Greetings").Delay(time.Hour).Reply(Message(&pb.HiRes{})))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := client(t, m).Greetings(ctx, &pb.HiReq{})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))

	m.Close()
	require.Zero(t, scope.Hits())
}
