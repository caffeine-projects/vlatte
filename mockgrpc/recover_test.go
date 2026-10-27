package mockgrpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// funcUnaryReply is a unary reply that calls fn.
type funcUnaryReply func() (any, error)

func (f funcUnaryReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return f, nil
}

func (f funcUnaryReply) Reply(_ context.Context, call *Call) error {
	res, err := f()
	if err != nil {
		return err
	}

	return call.Send(res.(proto.Message))
}

// funcStreamReply is a stream reply that calls fn.
type funcStreamReply func() error

func (f funcStreamReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return f, nil
}

func (f funcStreamReply) Reply(context.Context, *Call) error { return f() }

func client(t *testing.T, m *Server) pb.TestServiceClient {
	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return pb.NewTestServiceClient(conn)
}

func serve(t *testing.T) *Server {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

func TestReplyFailures_NameTheMock(t *testing.T) {
	// A reply that failed returned its error as is: Unknown, without saying which mock failed. It is a failure of
	// the server now, Internal with the reply failure trailer, and its message names the mock.
	m := serve(t)
	unary := m.MustMock(Method("Greetings").Reply(funcUnaryReply(func() (any, error) {
		return nil, errors.New("unary broke")
	})))
	stream := m.MustMock(Method("List").Reply(funcStreamReply(func() error {
		return errors.New("stream broke")
	})))
	c := client(t, m)

	var trailer metadata.MD
	_, err := c.Greetings(context.Background(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))
	require.Contains(t, status.Convert(err).Message(), "Mock ["+unary.IDs()[0]+"]")
	require.Contains(t, status.Convert(err).Message(), "unary broke")

	s, err := c.List(context.Background(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = s.Recv()
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, s.Trailer().Get(vlatte.HeaderFailure))
	require.Contains(t, status.Convert(err).Message(), "Mock ["+stream.IDs()[0]+"]")
	require.Contains(t, status.Convert(err).Message(), "stream broke")
}

func TestReplyFailures_AMockStatusIsTheMocksAnswer(t *testing.T) {
	// A mock that answers with a status, NotFound here, is serving its call: it must reach the client as set,
	// without the failure trailer, or a test of an error path can't tell it from a missing mock.
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Status(codes.NotFound, "no such user")))

	var trailer metadata.MD
	_, err := client(t, m).Greetings(context.Background(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, "no such user", status.Convert(err).Message())
	require.Empty(t, trailer.Get(vlatte.HeaderFailure))
}

func TestRecover(t *testing.T) {
	// A panicking replier crashed the whole server, failing every test sharing it. The call answers Internal with
	// the panic failure trailer now, and the server serves the next call.
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(funcUnaryReply(func() (any, error) { panic("unary boom") })))
	m.MustMock(Method("List").Reply(funcStreamReply(func() error { panic("stream boom") })))
	c := client(t, m)

	for range 2 {
		var trailer metadata.MD
		_, err := c.Greetings(context.Background(), &pb.HiReq{}, grpc.Trailer(&trailer))
		require.Equal(t, codes.Internal, status.Code(err))
		require.Equal(t, "panic: unary boom", status.Convert(err).Message())
		require.Equal(t, []string{vlatte.FailurePanic}, trailer.Get(vlatte.HeaderFailure))

		s, err := c.List(context.Background(), &pb.ListReq{})
		require.NoError(t, err)
		_, err = s.Recv()
		require.Equal(t, codes.Internal, status.Code(err))
		require.Equal(t, "panic: stream boom", status.Convert(err).Message())
		require.Equal(t, []string{vlatte.FailurePanic}, s.Trailer().Get(vlatte.HeaderFailure))
	}
}
