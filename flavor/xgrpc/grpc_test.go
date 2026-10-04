package xgrpc

import (
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/filter"
	pb "github.com/caffeine-projects/vlatte/flavor/xgrpc/internal/protobuf"
)

type blockingUnaryReply struct {
	entered chan struct{}
	release chan struct{}
}

func (r *blockingUnaryReply) Reply(_ context.Context, _ *UnaryRequestValues) (any, error) {
	close(r.entered)
	<-r.release
	return &pb.HiRes{Message: "bye"}, nil
}

func TestClose_BeforeStart(t *testing.T) {
	require.NotPanics(t, New().Close)
}

func TestClose_WaitsForInFlightCalls(t *testing.T) {
	m := New()
	pb.RegisterTestServiceServer(m.Server(), pb.UnimplementedTestServiceServer{})
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
	m := New(Setup().Logger(&logger))
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
			m := New(Setup().ServerOptions(opt))
			pb.RegisterTestServiceServer(m.Server(), pb.UnimplementedTestServiceServer{})
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
	m := New(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
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
	m := New()
	m.MustStart()
	t.Cleanup(m.Close)

	host, _, err := net.SplitHostPort(m.Addr())
	require.NoError(t, err)
	// Binding every interface exposes the mock to the network and triggers firewall prompts on macOS.
	require.Equal(t, "127.0.0.1", host)
}

func TestServerStream_MatchesRequestFields(t *testing.T) {
	m := New(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(ServerStreamMethod("List").
		Field("message", filter.Eq("hi")).
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
