package mockgrpc

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

type blockingUnaryReply struct {
	entered chan struct{}
	release chan struct{}
}

// NewReplier gives every mock this reply itself: the test waits on its channels.
func (r *blockingUnaryReply) NewReplier(*Server) (UnaryReplier, error) {
	return r, nil
}

func (r *blockingUnaryReply) Reply(_ context.Context, _ *UnaryRequestValues) (any, error) {
	close(r.entered)
	<-r.release
	return &pb.HiRes{Message: "bye"}, nil
}

func TestClose_BeforeStart(t *testing.T) {
	require.NotPanics(t, MustNew().Close)
}

func TestClose_WaitsForInFlightCalls(t *testing.T) {
	m := MustNew()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
	m.MustStart()

	reply := &blockingUnaryReply{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(reply.release) })
	t.Cleanup(release)
	m.MustMock(UnaryMethod("Greetings").Reply(reply))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	callDone := make(chan error, 1)
	go func() {
		_, err := pb.NewTestServiceClient(conn).Greetings(context.Background(), &pb.HiReq{Message: "hi"})
		callDone <- err
	}()
	<-reply.entered

	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()

	// Close is synchronous: it must not return while a call is still being served.
	select {
	case <-closed:
		t.Fatal("Close returned while a call was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	require.NoError(t, <-callDone)
	<-closed
}

func TestServeErrorsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	m.Close()

	// A gRPC server cannot be restarted: Serve fails, and the failure must be logged instead of swallowed.
	require.NoError(t, m.Start())
	m.Close()

	require.Contains(t, buf.String(), grpc.ErrServerStopped.Error())
}

func TestServerOptionsAreApplied(t *testing.T) {
	var calls atomic.Int32
	count := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		calls.Add(1)
		return handler(ctx, req)
	}

	for name, opt := range map[string]grpc.ServerOption{
		"chained interceptor": grpc.ChainUnaryInterceptor(count),
		"plain interceptor":   grpc.UnaryInterceptor(count),
	} {
		t.Run(name, func(t *testing.T) {
			calls.Store(0)
			m := MustNew(Setup().ServerOptions(opt))
			pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "bye"})))

			conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			_, err = pb.NewTestServiceClient(conn).Greetings(context.Background(), &pb.HiReq{Message: "hi"})
			require.NoError(t, err)
			// Server options from the configuration, like user interceptors, must be applied.
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestUnmatchedCallsReturnNotFound(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)

	// Calls no mock matches get NotFound, unary and streaming alike. Internal stays reserved for real errors.
	_, err = client.Greetings(context.Background(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, status.Code(err))

	stream, err := client.List(context.Background(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestDefaultAddrIsLoopback(t *testing.T) {
	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	host, _, err := net.SplitHostPort(m.Addr())
	require.NoError(t, err)
	// Binding every interface exposes the mock to the network and triggers firewall prompts on macOS.
	require.Equal(t, "127.0.0.1", host)
}

func TestServerStream_MatchesRequestFields(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(ServerStreamMethod("List").
		Field("message", match.Eq("hi")).
		Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)

	// Field matchers read the request message: a matching request is served...
	stream, err := client.List(context.Background(), &pb.ListReq{Message: "hi"})
	require.NoError(t, err)
	item, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "a", item.GetKey())

	// ...and a request with another value is not.
	stream, err = client.List(context.Background(), &pb.ListReq{Message: "other"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestServerStream_MatchesRequestHeader(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(ServerStreamMethod("List").
		Header("x-team", match.Eq("a")).
		Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)

	// Header matchers read the metadata of the call: a call with the header is served...
	stream, err := client.List(metadata.AppendToOutgoingContext(t.Context(), "x-team", "a"), &pb.ListReq{})
	require.NoError(t, err)
	item, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "a", item.GetKey())

	// ...and a call without it is not.
	stream, err = client.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))
}

type failingConfigurer struct{}

func (failingConfigurer) Apply(*Config) error { return errors.New("bad config") }

func TestNew_ReturnsErrors(t *testing.T) {
	_, err := New(failingConfigurer{})
	require.ErrorContains(t, err, "bad config")

	require.Panics(t, func() { MustNew(failingConfigurer{}) })
}

func TestListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).Listener(l))
	m.MustStart()
	t.Cleanup(m.Close)

	// The server serves the given listener instead of listening on Addr.
	require.Equal(t, l.Addr().String(), m.Addr())

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestUnaryBuilder_OneBuilderOnTwoServers(t *testing.T) {
	// Build returned the builder's own mock, so two servers served one mock, with one hit count and one ID. Each build
	// is a new mock now, with a replier of its own.
	b := UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "hi"}))

	servers := make([]*Server, 2)
	mocks := make([]*Mock, 2)

	for i := range servers {
		s := MustNew()
		pb.RegisterTestServiceServer(s, pb.UnimplementedTestServiceServer{})
		s.MustStart()
		t.Cleanup(s.Close)

		servers[i] = s
		mocks[i] = s.MustMock(b).Mocks()[0]
	}

	require.NotSame(t, mocks[0], mocks[1])
	require.NotEqual(t, mocks[0].ID(), mocks[1].ID())

	conn, err := grpc.NewClient(servers[0].Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	require.Equal(t, int64(1), mocks[0].Hits())
	require.Zero(t, mocks[1].Hits(), "hits are per mock")
}

// noReplier is a reply factory that makes no replier.
type noReplier struct{}

func (noReplier) NewReplier(*Server) (UnaryReplier, error) { return nil, nil }

func TestBuilder_ReplyErrors(t *testing.T) {
	// Build stored the reply as given, so a mock without a reply was added and failed every call, and a stream whose
	// reader failed streamed nothing, without an error. Build makes the replier of the mock now, and those fail it.
	m := MustNew()

	_, err := UnaryMethod("Greetings").Build(m)
	require.EqualError(t, err, "mock: no reply set: use Reply")

	_, err = UnaryMethod("Greetings").Reply(noReplier{}).Build(m)
	require.EqualError(t, err, "reply: the factory made no replier")

	_, err = ServerStreamMethod("List").
		Reply(ServerStream[*pb.ListItem]().JSON(iotest.ErrReader(errors.New("boom")))).
		Build(m)
	require.EqualError(t, err, "stream: reading the messages: boom")
}

func TestMocksOfBothKinds_EachCallFindsItsKind(t *testing.T) {
	// A mock has matchers for its own kind of call only, and no matchers match every call: a stream call matched a
	// unary mock added before the stream mock, and failed, and a unary call did the same with a stream mock added
	// first. Each call is matched against the mocks of its kind now.
	unary := UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "hi"}))
	stream := ServerStreamMethod("List").Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}}))

	for _, unaryFirst := range []bool{true, false} {
		m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
		m.MustStart()
		t.Cleanup(m.Close)

		if unaryFirst {
			m.MustMock(unary, stream)
		} else {
			m.MustMock(stream, unary)
		}

		conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		client := pb.NewTestServiceClient(conn)
		res, err := client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
		require.NoError(t, err, "unary first: %t", unaryFirst)
		require.Equal(t, "hi", res.Message)
		require.Equal(t, []string{"a"}, listKeys(t, client), "unary first: %t", unaryFirst)
	}
}
