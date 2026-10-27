package mockgrpc

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// proxied returns a server whose mocks proxy to target with r, for every method of the test service.
func proxied(t *testing.T, r *ProxyReply) *Server {
	t.Helper()

	m := serve(t)
	m.MustMock(Method("protobuf.TestService/Greetings").Reply(r))
	m.MustMock(Method("protobuf.TestService/List").Reply(r))
	m.MustMock(Method("protobuf.TestService/Upload").Reply(r))
	m.MustMock(Method("protobuf.TestService/Chat").Reply(r))

	return m
}

func TestProxy_UnaryCallsGetTheTargetsAnswer(t *testing.T) {
	// The client gets what the target answers: its message, header and trailer. The target gets the request and the
	// client's metadata, with the mock's own in place of the client's values.
	target := serve(t)
	target.MustMock(Method("Greetings").
		Field("message", match.Equal("hi")).
		Header("x-tenant", match.Equal("a")).
		Header("x-token", match.Equal("mock's")).
		Header("x-drop", match.Not(match.Present())).
		Reply(Message(&pb.HiRes{Message: "hello"}).Header("h", "v").Trailer("t", "w")))

	m := proxied(t, From(target.Addr()).Header("x-token", "mock's").RemoveHeaders("X-Drop"))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-tenant", "a", "x-token", "client's", "x-drop", "1")

	var header, trailer metadata.MD
	res, err := client(t, m).Greetings(ctx, &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)
	require.Equal(t, "hello", res.GetMessage())
	require.Equal(t, []string{"v"}, header.Get("h"))
	require.Equal(t, []string{"w"}, trailer.Get("t"))
}

func TestProxy_DebugHeadersNameTheProxyingMock(t *testing.T) {
	// A target that is a mock server sends its debug headers: the client gets the proxying mock's, once.
	target := serve(t)
	target.MustMock(Method("Greetings").Name("target").Reply(Message(&pb.HiRes{Message: "hello"})))

	m := serve(t)
	front := m.MustMock(Method("Greetings").Name("front").Reply(From(target.Addr()))).Mocks()[0]

	var header metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, []string{front.ID()}, header.Get(vlatte.HeaderMatchedMockID))
	require.Equal(t, []string{"front"}, header.Get(vlatte.HeaderMatchedMockName))
	require.Equal(t, []string{"1"}, header.Get(vlatte.HeaderMatchedMockHits))
}

func TestProxy_StreamsGoOnMessageByMessage(t *testing.T) {
	// Each call type goes through whole: the messages the client sends reach the target, and the target's answers
	// reach the client, in order, ending with the target's status.
	target := serve(t)
	target.MustMock(Method("List").
		Field("message", match.Equal("all")).
		Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).Status(codes.DataLoss, "cut")))
	target.MustMock(Method("Upload").
		JSON(`[{"key": "x"}, {"key": "y"}]`).
		Reply(Message(&pb.HiRes{Message: "2 items"})))
	target.MustMock(Method("Chat").Reply(Bidi().
		On(match.Field("message", match.Equal("hi")), Message(&pb.HiRes{Message: "hello"})).
		Otherwise(JSON(`{"message": "?"}`)).
		OnEnd(Message(&pb.HiRes{Message: "bye"}))))

	c := client(t, proxied(t, From(target.Addr())))

	t.Run("server streaming", func(t *testing.T) {
		stream, err := c.List(t.Context(), &pb.ListReq{Message: "all"})
		require.NoError(t, err)

		var keys []string
		for {
			item, err := stream.Recv()
			if err != nil {
				require.Equal(t, codes.DataLoss, status.Code(err))
				require.Equal(t, "cut", status.Convert(err).Message())

				break
			}

			keys = append(keys, item.GetKey())
		}

		require.Equal(t, []string{"a", "b"}, keys)
	})

	t.Run("client streaming", func(t *testing.T) {
		stream, err := c.Upload(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&pb.ListItem{Key: "x"}))
		require.NoError(t, stream.Send(&pb.ListItem{Key: "y"}))

		res, err := stream.CloseAndRecv()
		require.NoError(t, err)
		require.Equal(t, "2 items", res.GetMessage())
	})

	t.Run("bidi streaming", func(t *testing.T) {
		got, code := chat(t, c, nil, "hi", "what")
		require.Equal(t, codes.OK, code)
		require.Equal(t, []string{"hello", "?", "bye"}, got)
	})
}

func TestProxy_ABidiCallGoesOnAsTheTargetAnswers(t *testing.T) {
	// A bidi call is a conversation: the client waits for each answer before it sends its next message.
	target := serve(t)
	target.MustMock(Method("Chat").Reply(Func(func(_ context.Context, call *Call) error {
		for {
			msg, err := call.Recv()
			if err != nil {
				return nil
			}

			if err := call.Send(&pb.HiRes{Message: "re: " + msg.(*pb.HiReq).GetMessage()}); err != nil {
				return err
			}
		}
	})))

	m := proxied(t, From(target.Addr()))

	stream, err := client(t, m).Chat(t.Context())
	require.NoError(t, err)

	for _, msg := range []string{"a", "b"} {
		require.NoError(t, stream.Send(&pb.HiReq{Message: msg}))

		res, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "re: "+msg, res.GetMessage())
	}

	require.NoError(t, stream.CloseSend())

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestProxy_AClientThatLeavesEndsTheCallToTheTarget(t *testing.T) {
	// The target's call is the client's: a client that leaves a bidi call ends it at the target too, rather than
	// leave the target waiting for messages that never come.
	got := make(chan struct{})
	ended := make(chan error, 1)

	target := serve(t)
	target.MustMock(Method("Chat").Reply(Func(func(ctx context.Context, call *Call) error {
		if _, err := call.Recv(); err == nil {
			close(got)
		}

		_, err := call.Recv()
		ended <- ctx.Err()

		return err
	})))

	m := proxied(t, From(target.Addr()))

	ctx, cancel := context.WithCancel(t.Context())

	stream, err := client(t, m).Chat(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.HiReq{Message: "a"}))

	<-got
	cancel()

	require.ErrorIs(t, <-ended, context.Canceled)
}

func TestProxy_TheTargetsStatusIsTheAnswer(t *testing.T) {
	// A target that fails the call answers: the client gets its status, as from a mock that replies with Status.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Status(codes.PermissionDenied, "no").Trailer("t", "w")))

	m := proxied(t, From(target.Addr()))

	var trailer metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, "no", status.Convert(err).Message())
	require.Equal(t, []string{"w"}, trailer.Get("t"))
	require.Empty(t, trailer.Get(vlatte.HeaderFailure))
}

func TestProxy_ATargetThatCantBeReachedFailsTheCall(t *testing.T) {
	// A target that isn't there answers nothing: the call fails, so the client tells it from a target's Unavailable.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	m := proxied(t, From(addr))

	for name, call := range map[string]func(md *metadata.MD) error{
		"unary": func(md *metadata.MD) error {
			_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(md))
			return err
		},
		"server streaming": func(md *metadata.MD) error {
			stream, err := client(t, m).List(t.Context(), &pb.ListReq{})
			require.NoError(t, err)

			_, err = stream.Recv()
			*md = stream.Trailer()

			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			var trailer metadata.MD

			err := call(&trailer)
			require.Equal(t, codes.Internal, status.Code(err))
			require.Contains(t, status.Convert(err).Message(), "reply_proxy: target "+addr)
			require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))
		})
	}
}

func TestProxy_ANewMockNeedsATarget(t *testing.T) {
	m := serve(t)

	_, err := m.Mock(Method("Greetings").Reply(From("")))
	require.ErrorIs(t, err, errNoTarget)
}

func TestProxy_ATargetGRPCCantParseFailsTheMock(t *testing.T) {
	m := serve(t)

	_, err := m.Mock(Method("Greetings").Reply(From("dns:///%zz")))
	require.ErrorContains(t, err, `reply_proxy: target "dns:///%zz"`)
}

func TestProxy_DialOptionsReachTheTarget(t *testing.T) {
	// A target only a dialer of the caller's reaches: an in-memory server.
	target := MustNew(Setup().InMemory())
	target.MustStart()
	t.Cleanup(target.Close)
	target.MustMock(Method("protobuf.TestService/Greetings").Reply(Message(&pb.HiRes{Message: "in memory"})))

	m := proxied(t, From("passthrough:///memory").DialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return target.memory.DialContext(ctx)
	})))

	res, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "in memory", res.GetMessage())
}

func TestProxy_TheTargetGetsTheGRPCMetadataLeftToTheApplication(t *testing.T) {
	// gRPC reserves only some grpc- keys: a trace context and a retry's attempt count are the client's metadata, which
	// the target needs as the client sent it.
	got := make(chan metadata.MD, 1)

	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Func(func(ctx context.Context, call *Call) error {
		md, _ := metadata.FromIncomingContext(ctx)
		got <- md

		return call.Send(&pb.HiRes{})
	})))

	m := proxied(t, From(target.Addr()))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "grpc-trace-bin", "trace", "grpc-previous-rpc-attempts", "2")
	_, err := client(t, m).Greetings(ctx, &pb.HiReq{})
	require.NoError(t, err)

	md := <-got
	require.Equal(t, []string{"trace"}, md.Get("grpc-trace-bin"))
	require.Equal(t, []string{"2"}, md.Get("grpc-previous-rpc-attempts"))
}
