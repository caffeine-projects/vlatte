package mockgrpc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServer_MustStartReportsASecondStart(t *testing.T) {
	m := MustNew(Setup().Silent().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)
	require.PanicsWithError(t, "server: Start: "+vlatte.ErrStarted.Error(), m.MustStart)
}

type blockingUnaryReply struct {
	entered chan struct{}
	release chan struct{}
}

// NewReplier gives every mock this reply itself: the test waits on its channels.
func (r *blockingUnaryReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return r, nil
}

func (r *blockingUnaryReply) Reply(_ context.Context, call *Call) error {
	close(r.entered)
	<-r.release

	return call.Send(&pb.HiRes{Message: "bye"})
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

	require.ErrorContains(t, m.Start(), "creating the listener: ")
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
	m.MustMock(Method("Greetings").Reply(reply))

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

func TestClose_ClosesTheConnectionsOfWebhooks(t *testing.T) {
	// Listeners such as webhooks send through the transport of the Kit, the server's, not http.DefaultTransport:
	// Close closes its idle connections, so they don't outlive the server, as the HTTP server's don't.
	closed := make(chan struct{}, 1)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	target.Start()
	t.Cleanup(target.Close)

	logger := zerolog.Nop()
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").Callback(func(ctx context.Context, e vlatte.OnComplete) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, nil)
		if err != nil {
			return err
		}

		res, err := e.Kit().Transport().RoundTrip(req)
		if err != nil {
			return err
		}

		return res.Body.Close()
	}).Reply(Message(&pb.HiRes{Message: "hello"})))

	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	m.Close()

	select {
	case <-closed:
	case <-t.Context().Done():
		t.Fatal("the webhook's connection to its target outlived the server")
	}
}

func TestWebhooks_FollowTheOutboundRules(t *testing.T) {
	// Listeners such as webhooks send through the server's transport, which refuses what the outbound rules refuse:
	// a webhook's URL comes from a mock, which anyone who reaches the admin API sets.
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	logger := zerolog.Nop()
	m := MustNew(Setup().
		Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).
		Logger(&logger).
		OutboundDeny(netip.MustParsePrefix("127.0.0.0/8")))
	m.MustStart()
	t.Cleanup(m.Close)

	sent := make(chan error, 1)
	m.MustMock(Method("Greetings").Callback(func(ctx context.Context, e vlatte.OnComplete) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, nil)
		if err != nil {
			return err
		}

		res, err := e.Kit().Transport().RoundTrip(req)
		if err == nil {
			err = res.Body.Close()
		}

		sent <- err

		return nil
	}).Reply(Message(&pb.HiRes{Message: "hello"})))

	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	// The answer waits for the callback, so it sent already.
	select {
	case err := <-sent:
		require.ErrorIs(t, err, vlatte.ErrOutboundRefused)
	default:
		t.Fatal("the answer came before the callback ran")
	}

	require.Zero(t, hits.Load())
}

func TestClose_ReleasesWhatTheMocksHold(t *testing.T) {
	// A mock's replier may hold a connection, such as a proxy's: Close releases it, and logs one that fails to close,
	// as it returns nothing.
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()

	held := &closingReply{err: errors.New("still busy")}
	mock := m.MustMock(Method("Greetings").Reply(held)).Mocks()[0]

	m.Close()

	require.Equal(t, int32(1), held.closed.Load())
	require.Contains(t, buf.String(), "server: Close: closing mock "+mock.ID())
	require.Contains(t, buf.String(), "still busy")

	require.NoError(t, mock.Close(), "a mock closes once")
	require.Equal(t, int32(1), held.closed.Load())
}

// closingReply is a reply whose replier holds something to release: Close counts its calls and returns err.
type closingReply struct {
	err    error
	closed atomic.Int32
}

func (r *closingReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return r, nil
}

func (r *closingReply) Reply(context.Context, *Call) error { return nil }

func (r *closingReply) Close() error {
	r.closed.Add(1)
	return r.err
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
			m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "bye"})))

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

func TestConnectionTimeout_ClosesAConnectionWithoutTheClientPreface(t *testing.T) {
	tests := []struct {
		name  string
		setup *ConfigBuilder
		wait  time.Duration
	}{
		{
			name:  "configured through a flag",
			setup: Setup().ConfigSources(ConfigArgs([]string{"--grpc-connection-timeout=1s"})),
			wait:  time.Second,
		},
		{
			name:  "configured through Setup",
			setup: Setup().ConfigSources().ConnectionTimeout(time.Second),
			wait:  time.Second,
		},
		{
			name:  "default keeps the native timeout",
			setup: Setup().ConfigSources(),
			wait:  120 * time.Second,
		},
		{
			name:  "zero keeps the native timeout",
			setup: Setup().ConfigSources().ConnectionTimeout(0),
			wait:  120 * time.Second,
		},
		{
			name: "native options override the configured timeout",
			setup: Setup().ConfigSources().ConnectionTimeout(time.Second).
				ServerOptions(grpc.ConnectionTimeout(2 * time.Second)),
			wait: 2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				listener := bufconn.Listen(1024)
				m := MustNew(tt.setup.Silent().Listener(listener))
				m.MustStart()
				defer m.Close()

				conn, err := listener.DialContext(t.Context())
				require.NoError(t, err)
				defer conn.Close()

				start := time.Now()
				framer := http2.NewFramer(conn, conn)
				frame, err := framer.ReadFrame()
				require.NoError(t, err)
				require.IsType(t, &http2.SettingsFrame{}, frame)

				// A client that never finishes HTTP/2 setup must not hold the connection forever.
				_, err = framer.ReadFrame()
				require.ErrorIs(t, err, io.EOF)
				require.Equal(t, tt.wait, time.Since(start))
			})
		})
	}
}

func TestMaxConnectionIdle_AsksAnIdleClientToReconnect(t *testing.T) {
	tests := []struct {
		name  string
		setup *ConfigBuilder
		wait  time.Duration
	}{
		{
			name:  "configured through a flag",
			setup: Setup().ConfigSources(ConfigArgs([]string{"--grpc-max-connection-idle=1s"})),
			wait:  time.Second,
		},
		{
			name:  "configured through Setup",
			setup: Setup().ConfigSources().MaxConnectionIdle(time.Second),
			wait:  time.Second,
		},
		{
			name: "native options override the configured idle limit",
			setup: Setup().ConfigSources().MaxConnectionIdle(time.Second).
				ServerOptions(grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 2 * time.Second})),
			wait: 2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				listener := bufconn.Listen(1024)
				m := MustNew(tt.setup.Silent().Listener(listener))
				m.MustStart()
				defer m.Close()

				_, framer := connectHTTP2(t, listener)
				start := time.Now()
				frame, err := framer.ReadFrame()
				require.NoError(t, err)
				goaway, ok := frame.(*http2.GoAwayFrame)
				require.True(t, ok, "expected GOAWAY, got %T", frame)
				require.Equal(t, http2.ErrCodeNo, goaway.ErrCode)
				require.Equal(t, "max_idle", string(goaway.DebugData()))
				require.Equal(t, tt.wait, time.Since(start))
			})
		})
	}
}

func TestMaxConnectionIdle_ZeroKeepsIdleConnectionsAvailable(t *testing.T) {
	for name, setup := range map[string]*ConfigBuilder{
		"default":       Setup().ConfigSources(),
		"explicit zero": Setup().ConfigSources().MaxConnectionIdle(0),
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				listener := bufconn.Listen(1024)
				m := MustNew(setup.Silent().Listener(listener))
				m.MustStart()
				defer m.Close()

				conn, framer := connectHTTP2(t, listener)
				start := time.Now()
				require.NoError(t, conn.SetReadDeadline(start.Add(time.Minute)))
				_, err := framer.ReadFrame()
				netErr, ok := errors.AsType[net.Error](err)
				require.True(t, ok, err)
				require.True(t, netErr.Timeout(), err)
				require.Equal(t, time.Minute, time.Since(start))

				// No GOAWAY arrived, and the same idle connection still answers the client.
				require.NoError(t, conn.SetReadDeadline(time.Time{}))
				data := [8]byte{1}
				require.NoError(t, framer.WritePing(false, data))
				frame, err := framer.ReadFrame()
				require.NoError(t, err)
				ping, ok := frame.(*http2.PingFrame)
				require.True(t, ok, "expected PING acknowledgement, got %T", frame)
				require.True(t, ping.IsAck())
				require.Equal(t, data, ping.Data)
			})
		})
	}
}

func TestConnectionLimits_DoNotCutAnActiveSlowStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := bufconn.Listen(1024)
		m := MustNew(Setup().Silent().ConfigSources().Listener(listener).
			ConnectionTimeout(time.Second).MaxConnectionIdle(time.Second))
		m.MustStart()
		defer m.Close()
		m.MustMock(Method("List").Reply(Messages(
			&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"},
		).MessageDelay(2 * time.Second)))

		conn, err := grpc.NewClient("passthrough:///bufconn",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}))
		require.NoError(t, err)
		defer conn.Close()

		start := time.Now()
		stream, err := pb.NewTestServiceClient(conn).List(t.Context(), &pb.ListReq{})
		require.NoError(t, err)
		first, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "a", first.GetKey())
		require.Equal(t, 2*time.Second, time.Since(start))
		second, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "b", second.GetKey())
		require.Equal(t, 4*time.Second, time.Since(start))
		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF)
	})
}

func TestConnectionLimits_RejectNegativeDurations(t *testing.T) {
	for name, conf := range map[string]*Config{
		"connection_timeout":  {ConfigSources: []ConfigSource{}, ConnectionTimeout: -time.Second},
		"max_connection_idle": {ConfigSources: []ConfigSource{}, MaxConnectionIdle: -time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(conf)
			require.EqualError(t, err, "server: grpc."+name+": expected 0 or more, not -1s")
		})
	}
}

func connectHTTP2(t *testing.T, listener *bufconn.Listener) (net.Conn, *http2.Framer) {
	t.Helper()

	conn, err := listener.DialContext(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.Write([]byte(http2.ClientPreface))
	require.NoError(t, err)
	framer := http2.NewFramer(conn, conn)
	require.NoError(t, framer.WriteSettings())

	frame, err := framer.ReadFrame()
	require.NoError(t, err)
	require.IsType(t, &http2.SettingsFrame{}, frame)
	require.NoError(t, framer.WriteSettingsAck())

	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		if settings, ok := frame.(*http2.SettingsFrame); ok && settings.IsAck() {
			return conn, framer
		}
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
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))

	stream, err := client.List(context.Background(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, stream.Trailer().Get(vlatte.HeaderFailure))
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

	m.MustMock(Method("List").
		Field("message", match.Eq("hi")).
		Reply(Messages(&pb.ListItem{Key: "a"})))

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

	m.MustMock(Method("List").
		Header("x-team", match.Eq("a")).
		Reply(Messages(&pb.ListItem{Key: "a"})))

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

func TestNew_WithAJournalAStreamInterceptorGoesInTheChain(t *testing.T) {
	// The journal runs outside every interceptor, so it has the code the client gets. grpc runs the stream interceptor
	// set with grpc.StreamInterceptor outside all the others, so the journal takes it: New fails with one given in
	// ServerOptions, which grpc.ChainStreamInterceptor gives as well, rather than journal the codes it changes wrong.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	passStream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		return next(srv, ss)
	}
	pass := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		return next(ctx, req)
	}

	_, err := New(Setup().Journal("").ServerOptions(grpc.StreamInterceptor(passStream)))
	require.ErrorIs(t, err, ErrStreamInterceptorSlot)

	left, err := filepath.Glob(filepath.Join(tmp, "vlatte-journal-*"))
	require.NoError(t, err)
	require.Empty(t, left, "a server that fails to build removes its temporary journal")

	for name, setup := range map[string]*ConfigBuilder{
		"a stream interceptor without a journal":      Setup().ServerOptions(grpc.StreamInterceptor(passStream)),
		"a chained stream interceptor with a journal": Setup().Journal("").ServerOptions(grpc.ChainStreamInterceptor(passStream)),
		"a unary interceptor with a journal":          Setup().Journal("").ServerOptions(grpc.UnaryInterceptor(pass)),
	} {
		t.Run(name, func(t *testing.T) {
			m, err := New(setup)
			require.NoError(t, err)
			m.Close()
		})
	}
}

func TestServer_WarnsOfAServiceRegisteredPastTheJournal(t *testing.T) {
	// A service registered on GRPCServer, past the mock server, has its unary calls journaled within the interceptors,
	// with the code the mocks answered with: Start says so, and how to journal the code the client gets.
	tests := []struct {
		name     string
		register func(m *Server)
		warns    bool
	}{
		{
			name:     "on GRPCServer",
			register: func(m *Server) { pb.RegisterTestServiceServer(m.GRPCServer(), pb.UnimplementedTestServiceServer{}) },
			warns:    true,
		},
		{
			name:     "on the server",
			register: func(m *Server) { pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{}) },
		},
		{
			// A stream's journal is outside every interceptor wherever its service is registered.
			name: "on the server, with a service of streams on GRPCServer",
			register: func(m *Server) {
				pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
				reflection.Register(m.GRPCServer())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := zerolog.New(&buf)

			m := MustNew(Setup().Logger(&logger).Journal(""))
			tt.register(m)
			m.MustStart()
			t.Cleanup(m.Close)

			require.Equal(t, tt.warns, strings.Contains(buf.String(), "a service registered on GRPCServer"), buf.String())
			require.Equal(t, tt.warns, strings.Contains(buf.String(), `"service":"`+pb.TestService_ServiceDesc.ServiceName+`"`), buf.String())

			conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			_, err = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{})
			require.Equal(t, codes.NotFound, status.Code(err))

			n, err := m.VerifyRequests().Count(t.Context())
			require.NoError(t, err)
			require.Equal(t, 1, n, "the call is journaled either way")
		})
	}
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
	b := Method("Greetings").Reply(Message(&pb.HiRes{Message: "hi"}))

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

func (noReplier) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return nil, nil
}

func TestBuilder_ReplyErrors(t *testing.T) {
	// Build stored the reply as given, so a mock without a reply was added and failed every call, and a stream whose
	// messages didn't decode streamed nothing, without an error. Build makes the replier of the mock now, and those
	// fail it.
	m := MustNew()

	_, err := Method("Greetings").Build(m)
	require.EqualError(t, err, "mock: no reply set: use Reply")

	_, err = Method("Greetings").Reply(noReplier{}).Build(m)
	require.EqualError(t, err, "mock: Greetings: reply: the factory made no replier")

	_, err = Method("List").Reply(JSON(`{"nope": 1}`)).Build(m)
	require.ErrorContains(t, err, "mock: List: reply: /protobuf.TestService/List: json 0: not a protobuf.ListItem")
}

func TestMocksOfBothKinds_EachCallFindsItsKind(t *testing.T) {
	// A mock has matchers for its own kind of call only, and no matchers match every call: a stream call matched a
	// unary mock added before the stream mock, and failed, and a unary call did the same with a stream mock added
	// first. Each call is matched against the mocks of its kind now.
	unary := Method("Greetings").Reply(Message(&pb.HiRes{Message: "hi"}))
	stream := Method("List").Reply(Messages(&pb.ListItem{Key: "a"}))

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

func TestClose_CutsTheCallsThatOutlastTheShutdownTimeout(t *testing.T) {
	// Close waited for every call: a stream whose client never closed it held Close, and the test, forever. The
	// clock is synctest's, which moves once every goroutine waits: the timeout passes at once.
	synctest.Test(t, func(t *testing.T) {
		m := MustNew(Setup().Silent().InMemory().ShutdownTimeout(time.Minute))
		m.MustStart()
		m.MustMock(Method("Chat").Reply(Bidi()))

		// A client of its own: Close closes those of Dial first, which would end the stream.
		conn, err := grpc.NewClient("passthrough:///bufconn",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return m.memory.DialContext(ctx) }))
		require.NoError(t, err)

		defer conn.Close()

		stream, err := pb.NewTestServiceClient(conn).Chat(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&pb.HiReq{Message: "never closes"}))

		start := time.Now()
		m.Close()
		require.Equal(t, time.Minute, time.Since(start).Round(time.Second), "Close waited for the timeout, then cut the call")

		_, err = stream.Recv()
		require.Equal(t, codes.Unavailable, status.Code(err), "the call was cut: %v", err)
	})
}

func TestNoMatchCode_IsTheCodeOfAnUnmatchedCall(t *testing.T) {
	// A call no mock matched answered NotFound, which an app may take for the answer of a real server: the code is
	// the server's to choose, as HTTP mock servers choose their status.
	m := MustNew(Setup().Silent().NoMatchCode(codes.Unimplemented))
	m.MustStart()
	t.Cleanup(m.Close)

	var trailer metadata.MD
	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
}

func TestNew_AGlobalEventOfNoListenerFails(t *testing.T) {
	// A global event runs for every mock: one that names no listener fails New, rather than every call.
	_, err := New(Setup().Silent().GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "nobody"}}}))
	require.ErrorContains(t, err, "server: global on_complete listener nobody at index 0 is not registered")
}

func TestNew_TemplateEnvAllowsVariablesOnTheDefaultEngine(t *testing.T) {
	m, err := New(Setup().ConfigSources(ConfigArgs([]string{"--grpc-template-env=VLATTE_TEST_ALLOWED"})))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	_, err = m.Config().TemplateEngine.Compile("body", `{{ env "VLATTE_TEST_ALLOWED" }}`)
	require.NoError(t, err)

	_, err = m.Config().TemplateEngine.Compile("body", `{{ env "VLATTE_TEST_OTHER" }}`)
	require.ErrorContains(t, err, `env: "VLATTE_TEST_OTHER" is not allowed`)
}

func TestNew_TemplateEnvWithAnEngineOfTheUsersOwnFails(t *testing.T) {
	// The variables would silently stay unreadable: the server can't change an engine it didn't make.
	_, err := New(Setup().TemplateEngine(vlatte.MustNewGoTemplateEngine()).TemplateEnv("X"))
	require.ErrorIs(t, err, errTemplateEnvWithEngine)
	require.ErrorContains(t, err, "grpc.template.env")
}

func TestNew_TemplateEnvFromGoRefusesAnInvalidName(t *testing.T) {
	_, err := New(Setup().TemplateEnv("A=B"))
	require.ErrorContains(t, err, `environment variable name "A=B" is invalid`)
}

func TestNew_TemplateKeysGiveTheDefaultEngineKeys(t *testing.T) {
	m, err := New(Setup().ConfigSources(ConfigArgs([]string{"--grpc-template-keys=k=ES256"})))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	_, err = m.Config().TemplateEngine.Compile("body", `{{ jwks "k" }}`)
	require.NoError(t, err)

	_, err = m.Config().TemplateEngine.Compile("body", `{{ jwks "other" }}`)
	require.ErrorContains(t, err, `"other" is not a signing key of the server`)
}

func TestNew_TemplateKeysWithAnEngineOfTheUsersOwnFails(t *testing.T) {
	_, err := New(Setup().TemplateEngine(vlatte.MustNewGoTemplateEngine()).TemplateKeys("k=ES256"))
	require.ErrorIs(t, err, errTemplateKeysWithEngine)
	require.ErrorContains(t, err, "grpc.template.keys")
}

func TestNew_TemplateKeysReadAFileRelativeToRootDir(t *testing.T) {
	dir := t.TempDir()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ec.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))

	m, err := New(Setup().RootDir(dir).TemplateKeys("k=ES256:ec.pem"))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	_, err = New(Setup().TemplateKeys("k=ES256:" + filepath.Join(dir, "missing.pem")))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPrep(t *testing.T) {
	m := MustNew()
	defer m.Close()
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})

	err := m.Prep(t.Context(), vlatte.RecipeFunc[*Mock, *Server](
		func(_ context.Context, in *vlatte.RecipeInput[*Mock, *Server]) error {
			_, err := in.Server.Mock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "from recipe"})))
			return err
		}))
	require.NoError(t, err)
	m.MustStart()

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// The mock a recipe adds serves calls like any other mock.
	res, err := pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)
	require.Equal(t, "from recipe", res.Message)
}

type failingOption struct{}

func (failingOption) Apply(*Config) error { return errors.New("bad config") }

func TestNew_AFailingOptionIsNamedByItsPlace(t *testing.T) {
	_, err := New(Setup(), failingOption{})
	require.EqualError(t, err, "server: applying option 1 (mockgrpc.failingOption): bad config")
}

func TestPrep_AFailingRecipeIsNamedByItsPlace(t *testing.T) {
	m := MustNew()
	defer m.Close()

	errRecipe := errors.New("recipe failed")
	err := m.Prep(t.Context(),
		vlatte.RecipeFunc[*Mock, *Server](func(context.Context, *vlatte.RecipeInput[*Mock, *Server]) error { return nil }),
		vlatte.RecipeFunc[*Mock, *Server](func(context.Context, *vlatte.RecipeInput[*Mock, *Server]) error { return errRecipe }))

	require.ErrorIs(t, err, errRecipe)
	require.EqualError(t, err, "server: applying recipe 1 (vlatte.RecipeFunc[*github.com/caffeine-projects/vlatte/mockgrpc.Mock,*github.com/caffeine-projects/vlatte/mockgrpc.Server]): recipe failed")
}

func TestServer_KVIsTheStoreTemplatesRead(t *testing.T) {
	// Go code sets the values templates read through KV, or Config().KV: the same store, which the server makes when
	// none is given.
	m := serve(t)

	require.Same(t, m.Config().KV, m.KV())
	require.NoError(t, m.KV().Set(t.Context(), "greeting", []byte("hello")))

	m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ .KV.Get "greeting" }}"}`)))

	got, code := greetWith(t, client(t, m), "ann")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hello", got)
}
