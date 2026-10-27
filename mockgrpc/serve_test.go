package mockgrpc

import (
	"context"
	"errors"
	"io"
	"iter"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServer_ServesAServiceNoOneRegistered(t *testing.T) {
	// Mocking a service took registering an implementation of it, such as pb.UnimplementedTestServiceServer, on the
	// server: a server without it answered Unimplemented, whatever its mocks. Its generated code is enough now.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Method("Greetings").Reply(Message(&pb.HiRes{Message: "unary"})),
		Method("List").Reply(Messages(&pb.ListItem{Key: "a"})),
		Method("Upload").Reply(Message(&pb.HiRes{Message: "client"})),
	)

	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "unary", got)
	require.Equal(t, []string{"a"}, listKeys(t, c))

	got, _ = upload(t, c, "x")
	require.Equal(t, "client", got)
}

func TestServer_AnUnknownMethodIsUnimplemented(t *testing.T) {
	// No mock can answer a method no descriptor describes: the answer says so, as the server's own.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	var trailer metadata.MD
	err := m.MustDial().Invoke(t.Context(), "/nope.Service/Call", &pb.HiReq{}, &pb.HiRes{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
}

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
		require.Equal(t, unary.IDs(), header.Get(vlatte.HeaderMatchedMockID))
		require.Equal(t, []string{"greet"}, header.Get(vlatte.HeaderMatchedMockName))
		require.Equal(t, []string{hits}, header.Get(vlatte.HeaderMatchedMockHits), "the hits count this call")
		require.Equal(t, []string{"v1"}, header.Get("k1"), "next to the mock's own header")
	}

	header := streamHeader(t, c)
	require.Equal(t, stream.IDs(), header.Get(vlatte.HeaderMatchedMockID))
	require.Equal(t, []string{"1"}, header.Get(vlatte.HeaderMatchedMockHits), "each mock counts its own")
	require.Empty(t, header.Get(vlatte.HeaderMatchedMockName), "a mock with no name sends none")
}

func TestDebugHeader_AMockThatAnswersWithAStatus(t *testing.T) {
	// A status is the mock's answer, so it names its mock as a message does.
	m := serve(t)
	scope := m.MustMock(Method("Greetings").Reply(Status(codes.NotFound, "no such user")))

	var header metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, scope.IDs(), header.Get(vlatte.HeaderMatchedMockID))
}

func TestDebugHeader_NoneWhenNoMockMatched(t *testing.T) {
	// No mock answered, so none is named: the failure trailer says the server answered.
	m := serve(t)

	var header, trailer metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, header.Get(vlatte.HeaderMatchedMockID))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
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
		for _, key := range []string{vlatte.HeaderMatchedMockID, vlatte.HeaderMatchedMockName, vlatte.HeaderMatchedMockHits} {
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
	require.Empty(t, header.Get(vlatte.HeaderMatchedMockHits))
	require.Empty(t, header.Get(vlatte.HeaderMatchedMockID))
	require.Zero(t, scope.Hits())

	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: `"ok"`}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, "ok", res.GetMessage())
	require.Equal(t, []string{"1"}, header.Get(vlatte.HeaderMatchedMockHits))
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
	require.Equal(t, []string{"1"}, header.Get(vlatte.HeaderMatchedMockHits))

	_, err = stream.Recv()
	require.NoError(t, err)

	cancel()

	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
	require.Eventually(t, func() bool { return scope.Hits() == 0 }, time.Second, time.Millisecond)

	require.Equal(t, []string{"1"}, streamHeader(t, c).Get(vlatte.HeaderMatchedMockHits))
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

// failingStore is a store whose List fails.
type failingStore struct {
	*vlatte.MemoryStore[*Mock]
}

func (failingStore) List(context.Context) (iter.Seq[*Mock], error) {
	return nil, errors.New("store down")
}

// requireMatchFailure asserts err and trailer are the answer to a call the server failed to find a mock for.
func requireMatchFailure(t *testing.T, err error, trailer metadata.MD, msg ...string) {
	t.Helper()

	require.Equal(t, codes.Internal, status.Code(err), "%v", err)
	require.Equal(t, []string{vlatte.FailureMatch}, trailer.Get(vlatte.HeaderFailure))

	for _, m := range msg {
		require.Contains(t, status.Convert(err).Message(), m)
	}
}

func TestFailures_FindingAMock(t *testing.T) {
	// A server that fails to find a mock answers Internal with the match failure, so a test tells it from a missing
	// mock (NotFound, no-match) and from a mock's answer.
	t.Run("a matcher fails", func(t *testing.T) {
		m := serve(t)
		broken := match.Function(func(any) (bool, error) { return false, errors.New("matcher failed") })
		unary := m.MustMock(Method("Greetings").Field("message", broken).Reply(Message(&pb.HiRes{})))
		stream := m.MustMock(Method("List").Field("message", broken).Reply(Messages()))
		c := client(t, m)

		var trailer metadata.MD
		_, err := c.Greetings(context.Background(), &pb.HiReq{Message: "hi"}, grpc.Trailer(&trailer))
		requireMatchFailure(t, err, trailer, "Mock ["+unary.IDs()[0]+"]", "matcher failed")

		s, err := c.List(context.Background(), &pb.ListReq{Message: "hi"})
		require.NoError(t, err)
		_, err = s.Recv()
		requireMatchFailure(t, err, s.Trailer(), "Mock ["+stream.IDs()[0]+"]", "matcher failed")
	})

	t.Run("the store fails", func(t *testing.T) {
		m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
		// Before Start, so serving never races the swap.
		m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}
		m.MustStart()
		t.Cleanup(m.Close)
		c := client(t, m)

		var trailer metadata.MD
		_, err := c.Greetings(context.Background(), &pb.HiReq{}, grpc.Trailer(&trailer))
		requireMatchFailure(t, err, trailer, "store down")

		s, err := c.List(context.Background(), &pb.ListReq{})
		require.NoError(t, err)
		_, err = s.Recv()
		requireMatchFailure(t, err, s.Trailer(), "store down")
	})
}
