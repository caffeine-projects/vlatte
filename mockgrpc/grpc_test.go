package mockgrpc

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

	"github.com/caffeine-projects/vlatte"
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

func TestServer_AClosedServerDoesntStartAgain(t *testing.T) {
	// A closed server doesn't start again, even one that never started.
	t.Run("after a start", func(t *testing.T) {
		m := MustNew()
		m.MustStart()
		m.Close()

		require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)
	})

	t.Run("before any start", func(t *testing.T) {
		m := MustNew()
		m.Close()

		require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)
		require.Empty(t, m.Addr(), "nothing listens")
	})
}

func TestServer_StartsOnce(t *testing.T) {
	// A second Start served a second listener and moved Addr: a server starts once, as the HTTP one does.
	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	first := m.Addr()

	require.ErrorIs(t, m.Start(), vlatte.ErrStarted)
	require.Equal(t, first, m.Addr(), "the server serves where it started")
}

func TestServer_AStartThatFailsClosesTheServer(t *testing.T) {
	// As the HTTP server does, a start that fails closes the server: it doesn't start again.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	m := MustNew(Setup().Addr(taken.Addr().String()))
	t.Cleanup(m.Close)

	require.ErrorContains(t, m.Start(), "failed to create listener")
	require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)
}

func TestServer_ACloseThatOverlapsStartLeavesNothingServing(t *testing.T) {
	// A Close that came while Start listened found nothing started and returned, then Start served (#57): once Close
	// returns, nothing listens, whatever Start was doing.
	for i := range 200 {
		m := MustNew()

		started := make(chan error, 1)
		go func() { started <- m.Start() }()

		// Let Start get going, so Close may come while it listens.
		time.Sleep(time.Duration(i%50) * time.Microsecond)
		m.Close()

		if err := <-started; err != nil {
			require.ErrorIs(t, err, grpc.ErrServerStopped, "iteration %d", i)
			continue
		}

		conn, err := net.DialTimeout("tcp", m.Addr(), time.Second)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("iteration %d: after Close, %s still accepts connections", i, m.Addr())
		}
	}
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

var errAccept = errors.New("accept failed")

// failingListener fails every Accept, which ends Serve.
type failingListener struct {
	net.Listener
}

func (failingListener) Accept() (net.Conn, error) {
	return nil, errAccept
}

func TestServeErrorsAreLogged(t *testing.T) {
	// A listener that fails ends Serve: the failure must be logged instead of swallowed.
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Logger(&logger).Listener(failingListener{Listener: inner}))
	m.MustStart()
	t.Cleanup(m.Close)

	// Close first would stop the server before Serve, which then fails for that instead.
	select {
	case <-m.served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept going after its listener failed")
	}

	require.Contains(t, buf.String(), errAccept.Error())
}

func TestClose_LogsAJournalThatFailsToClose(t *testing.T) {
	// Close returns nothing: a journal that fails to write the index of its last segment, which the next server of
	// its directory opens fast from, must be logged, not swallowed.
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	dir := t.TempDir()
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).Logger(&logger).Journal(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.NotFound, status.Code(err))

	// A directory where the index of the segment goes: the journal can't write it.
	idx := filepath.Join(dir, "journal-000001.idx")
	require.NoError(t, os.Mkdir(idx, 0o755))

	m.Close()

	type logLine struct {
		Level   string `json:"level"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}

	var closing []logLine
	for line := range strings.Lines(buf.String()) {
		var l logLine
		require.NoError(t, json.Unmarshal([]byte(line), &l), line)

		if l.Message == "server: Close: closing the journal" {
			closing = append(closing, l)
		}
	}

	require.Len(t, closing, 1, buf.String())
	require.Equal(t, "warn", closing[0].Level)
	require.True(t, strings.HasSuffix(closing[0].Error, " "+idx+": "+syscall.EEXIST.Error()), closing[0].Error)
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
	// A mock can answer NotFound too, so the failure trailer says the server answered, not a mock.
	var trailer metadata.MD
	_, err = client.Greetings(context.Background(), &pb.HiReq{Message: "hi"}, grpc.Trailer(&trailer))
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.FailureHeader))

	stream, err := client.List(context.Background(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, stream.Trailer().Get(vlatte.FailureHeader))
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

func TestNew_AJournalThatDoesntOpenFailsTheServer(t *testing.T) {
	// A server without the journal it was given keeps no call a test can verify: New fails, naming what is in the
	// way.
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	_, err := New(Setup().Journal(filepath.Join(file, "journal")))
	require.ErrorIs(t, err, syscall.ENOTDIR)

	pathErr, ok := errors.AsType[*fs.PathError](err)
	require.True(t, ok, err)
	require.Equal(t, file, pathErr.Path)
}

func TestNew_AServerThatFailsToBuildRemovesItsTemporaryJournal(t *testing.T) {
	// grpc panics on server options it can't take, two plain interceptors, once New opened the journal: a temporary
	// one goes with the server, or each such test leaves a directory behind in the system's temporary one.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	pass := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(ctx, req)
	}

	require.Panics(t, func() {
		_, _ = New(Setup().ServerOptions(grpc.UnaryInterceptor(pass), grpc.UnaryInterceptor(pass)).Journal(""))
	})

	left, err := filepath.Glob(filepath.Join(tmp, "vlatte-journal-*"))
	require.NoError(t, err)
	require.Empty(t, left)
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
