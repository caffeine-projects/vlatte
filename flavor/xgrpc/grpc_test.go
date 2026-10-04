package xgrpc

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
