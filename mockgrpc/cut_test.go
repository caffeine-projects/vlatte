package mockgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// wireErrs keeps every error the connections it dials read or write: a reset reaches the client on either.
type wireErrs struct {
	mu   sync.Mutex
	errs []error
}

func (w *wireErrs) dial(ctx context.Context, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	return &errConn{Conn: conn, w: w}, nil
}

// reset reports whether a connection read a reset.
func (w *wireErrs) reset() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return errors.Is(errors.Join(w.errs...), syscall.ECONNRESET)
}

type errConn struct {
	net.Conn

	w *wireErrs
}

func (c *errConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.keep(err)

	return n, err
}

func (c *errConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.keep(err)

	return n, err
}

func (c *errConn) keep(err error) {
	if err == nil {
		return
	}

	c.w.mu.Lock()
	defer c.w.mu.Unlock()

	c.w.errs = append(c.w.errs, err)
}

// listCut serves List with a and b, then cuts the call with cause.
func listCut(cause error) Reply {
	return Func(func(_ context.Context, call *Call) error {
		for _, key := range []string{"a", "b"} {
			if err := call.Send(&pb.ListItem{Key: key}); err != nil {
				return err
			}
		}

		return cause
	})
}

// listAll reads the List stream of c to its end, and returns the keys it read with the error it ended with.
func listAll(t *testing.T, c pb.TestServiceClient, opts ...grpc.CallOption) ([]string, error) {
	t.Helper()

	stream, err := c.List(t.Context(), &pb.ListReq{}, opts...)
	require.NoError(t, err)

	var keys []string
	for {
		item, err := stream.Recv()
		if err != nil {
			return keys, err
		}

		keys = append(keys, item.GetKey())
	}
}

func TestCut_AStreamEndsAfterWhatTheReplySentWithoutAStatus(t *testing.T) {
	// A client must handle a stream that breaks off midway, as a server that crashes or a proxy that drops it leaves
	// it: the messages sent reach it, then the connection ends, with no status and no trailer.
	tests := []struct {
		name  string
		cause error
		tls   bool
		reset bool
	}{
		{name: "abort", cause: vlatte.ErrAbort},
		{name: "reset", cause: vlatte.ErrReset, reset: true},
		{name: "wrapped reset", cause: fmt.Errorf("upstream: %w", vlatte.ErrReset), reset: true},
		{name: "abort over TLS", cause: vlatte.ErrAbort, tls: true},
		{name: "reset over TLS", cause: vlatte.ErrReset, tls: true, reset: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			if tc.tls {
				m.MustStartTLS()
			} else {
				m.MustStart()
			}

			t.Cleanup(m.Close)

			scope := m.MustMock(Method("List").Reply(listCut(tc.cause)))

			var wire wireErrs
			c := pb.NewTestServiceClient(m.MustDial(grpc.WithContextDialer(wire.dial)))

			var trailer metadata.MD
			keys, err := listAll(t, c, grpc.Trailer(&trailer))

			require.Equal(t, []string{"a", "b"}, keys)
			require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
			require.Empty(t, trailer, "the call ended with no status: the client read no trailer")
			require.Equal(t, tc.reset, wire.reset(), "only ErrReset resets the connection: %v", err)
			require.EqualValues(t, 1, scope.Hits(), "a cut is the mock's answer: the call counts")
		})
	}
}

func TestCut_CutsEveryCallType(t *testing.T) {
	// A reply cuts the call whatever its type: those whose client sends a stream end as those whose server does.
	m := serve(t)

	cut := Func(func(_ context.Context, call *Call) error {
		if call.Descriptor().IsStreamingServer() {
			if err := call.Send(&pb.HiRes{Message: "sent"}); err != nil {
				return err
			}
		}

		return vlatte.ErrAbort
	})

	m.MustMock(Method("Greetings").Reply(cut), Method("Upload").Reply(cut), Method("Chat").Reply(cut))

	_, code := greetWith(t, client(t, m), "hi")
	require.Equal(t, codes.Unavailable, code)

	up, err := client(t, m).Upload(t.Context())
	require.NoError(t, err)
	require.NoError(t, up.Send(&pb.ListItem{Key: "k"}))

	_, err = up.CloseAndRecv()
	require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)

	said, code := chat(t, client(t, m), nil, "hi")
	require.Equal(t, codes.Unavailable, code)
	require.Equal(t, []string{"sent"}, said)
}

func TestCut_TheServerServesTheCallsThatFollow(t *testing.T) {
	// A cut ends a connection, not the server: a client that connects again is served.
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("List").Reply(listCut(vlatte.ErrReset)))
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "back"})))

	_, err := listAll(t, c)
	require.Equal(t, codes.Unavailable, status.Code(err))

	got, code := greetWith(t, c, "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "back", got)
}

func TestCut_CutsAnInMemoryConnection(t *testing.T) {
	// An in-memory connection has no TCP to reset: a reset closes it, as an abort does.
	m := MustNew(Setup().Silent().InMemory().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("List").Reply(listCut(vlatte.ErrReset)))

	keys, err := listAll(t, pb.NewTestServiceClient(m.MustDial()))
	require.Equal(t, []string{"a", "b"}, keys)
	require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
}

func TestCut_TheEventsAndTheJournalSeeTheCut(t *testing.T) {
	// A cut is the mock's answer: its events run before the connection goes, and the journal keeps the call as matched,
	// with what the client got, Unavailable, and why.
	statuses := make(chan *status.Status, 1)

	m, c := journaled(t, Setup())
	m.MustMock(Method("List").Reply(listCut(vlatte.ErrAbort)).
		Callback(func(_ context.Context, e vlatte.OnComplete) error {
			statuses <- e.(*OnComplete).Status()
			return nil
		}))

	_, err := listAll(t, c)
	require.Equal(t, codes.Unavailable, status.Code(err))

	require.Equal(t, codes.Unavailable, (<-statuses).Code())

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeMatched, e.Outcome)
	require.Equal(t, int(codes.Unavailable), e.Status)
	require.Equal(t, vlatte.ErrAbort.Error(), e.Message)
}

func TestCut_FailsWithTheCredentialsOfServerOptions(t *testing.T) {
	// The server cuts the connections it handshakes: those of grpc.Creds it doesn't see, so the call fails, telling
	// why, instead of ending as if the mock answered.
	m := MustNew(Setup().Silent().
		Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).
		ServerOptions(grpc.Creds(insecure.NewCredentials())))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Method("List").Reply(listCut(vlatte.ErrAbort)))

	var trailer metadata.MD
	keys, err := listAll(t, client(t, m), grpc.Trailer(&trailer))

	require.Equal(t, []string{"a", "b"}, keys)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "grpc.Creds")
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(_failureKey))
	require.Zero(t, scope.Hits())
}

// wire is a connection that keeps what is written on it.
type wire struct {
	net.Conn

	out    bytes.Buffer
	closed bool
}

func (w *wire) Write(p []byte) (int, error) { return w.out.Write(p) }

func (w *wire) Close() error {
	w.closed = true
	return nil
}

func (w *wire) RemoteAddr() net.Addr { return &net.TCPAddr{} }

// frames writes, as grpc would, the frames fn writes with a framer and an HPACK encoder, and returns them.
func frames(t *testing.T, fn func(fr *http2.Framer, block func(fields ...string) []byte)) []byte {
	t.Helper()

	var out, buf bytes.Buffer
	enc := hpack.NewEncoder(&buf)
	fr := http2.NewFramer(&out, nil)

	fn(fr, func(fields ...string) []byte {
		buf.Reset()
		for i := 0; i < len(fields); i += 2 {
			require.NoError(t, enc.WriteField(hpack.HeaderField{Name: fields[i], Value: fields[i+1]}))
		}

		return bytes.Clone(buf.Bytes())
	})

	return out.Bytes()
}

func TestCutConn_CutsAtTheTrailerWhateverHowItsFramesAreWritten(t *testing.T) {
	// grpc writes frames in pieces of any size, and may split a header block over CONTINUATION frames, pad it or give
	// it a priority: what precedes the trailer of the cut goes out whole, and nothing from it.
	before := frames(t, func(fr *http2.Framer, block func(...string) []byte) {
		require.NoError(t, fr.WriteSettings())

		head := block(":status", "200", "content-type", "application/grpc")
		require.NoError(t, fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      1,
			BlockFragment: head[:2],
			PadLength:     3,
			Priority:      http2.PriorityParam{Weight: 7},
		}))
		require.NoError(t, fr.WriteContinuation(1, true, head[2:]))
		require.NoError(t, fr.WriteData(1, false, []byte("message")))
	})

	// The trailer's block has an encoder of its own, which refers to none of the fields the first block added to the
	// table: the connection decodes it as grpc's would.
	trailer := frames(t, func(fr *http2.Framer, block func(...string) []byte) {
		require.NoError(t, fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      1,
			BlockFragment: block("grpc-status", "14", _cutKey, "token"),
			EndHeaders:    true,
			EndStream:     true,
		}))
		require.NoError(t, fr.WriteRSTStream(1, http2.ErrCodeNo))
	})

	w := &wire{}
	c := newCutConn(w, w, func(net.Addr) {})
	c.cutAt("token", vlatte.ErrAbort)

	for _, b := range before {
		n, err := c.Write([]byte{b})
		require.NoError(t, err)
		require.Equal(t, 1, n)
	}

	require.Equal(t, before, w.out.Bytes())
	require.False(t, w.closed)

	_, err := c.Write(trailer)
	require.ErrorIs(t, err, errConnCut)
	require.True(t, w.closed)
	require.Equal(t, before, w.out.Bytes(), "the trailer of the cut never goes out")

	_, err = c.Write([]byte("more"))
	require.ErrorIs(t, err, errConnCut)
}

func TestCutConn_PassesOnWhatItCantRead(t *testing.T) {
	// Frames the connection can't read still reach the client as grpc wrote them: a cut then cuts at once, without
	// waiting for a trailer it can't find.
	tests := []struct {
		name  string
		write func(fr *http2.Framer, block func(...string) []byte) error
	}{
		{
			name: "a header block broken off",
			write: func(fr *http2.Framer, block func(...string) []byte) error {
				if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block("a", "b")}); err != nil {
					return err
				}

				return fr.WriteData(1, true, []byte("data"))
			},
		},
		{
			name: "a field HPACK can't decode",
			write: func(fr *http2.Framer, _ func(...string) []byte) error {
				return fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: []byte{0x80}, EndHeaders: true})
			},
		},
		{
			name: "a block that ends midway",
			write: func(fr *http2.Framer, _ func(...string) []byte) error {
				return fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: []byte{0x40}, EndHeaders: true})
			},
		},
		{
			name: "a padding longer than its frame",
			write: func(fr *http2.Framer, _ func(...string) []byte) error {
				return fr.WriteRawFrame(http2.FrameHeaders, http2.FlagHeadersPadded|http2.FlagHeadersEndHeaders, 1, []byte{9})
			},
		},
		{
			name: "a priority cut short",
			write: func(fr *http2.Framer, _ func(...string) []byte) error {
				return fr.WriteRawFrame(http2.FrameHeaders, http2.FlagHeadersPriority|http2.FlagHeadersEndHeaders, 1, []byte{0})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sent := frames(t, func(fr *http2.Framer, block func(...string) []byte) {
				require.NoError(t, tc.write(fr, block))
			})

			w := &wire{}
			c := newCutConn(w, w, func(net.Addr) {})

			n, err := c.Write(sent)
			require.NoError(t, err)
			require.Equal(t, len(sent), n)

			n, err = c.Write([]byte("rest"))
			require.NoError(t, err)
			require.Equal(t, 4, n)
			require.Equal(t, append(sent, "rest"...), w.out.Bytes())

			c.cutAt("token", vlatte.ErrAbort)
			require.True(t, w.closed)
		})
	}
}

func TestCutConn_GivingUpCutsACutThatWaits(t *testing.T) {
	// A cut that waits for its trailer when the frames turn unreadable would wait forever: it cuts there.
	w := &wire{}
	c := newCutConn(w, w, func(net.Addr) {})
	c.cutAt("token", vlatte.ErrAbort)

	_, err := c.Write(frames(t, func(fr *http2.Framer, _ func(...string) []byte) {
		require.NoError(t, fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      1,
			BlockFragment: []byte{0x80},
			EndHeaders:    true,
		}))
	}))
	require.ErrorIs(t, err, errConnCut)
	require.True(t, w.closed)
}

func TestServerCreds_FindTheConnectionOfACallByItsPeer(t *testing.T) {
	// Every in-memory connection has the same address: a call finds its own all the same, and none once it closed.
	creds := &serverCreds{TransportCredentials: insecure.NewCredentials()}

	a, _ := net.Pipe()
	b, _ := net.Pipe()

	ca, _, err := creds.ServerHandshake(a)
	require.NoError(t, err)

	cb, _, err := creds.ServerHandshake(b)
	require.NoError(t, err)

	callOn := func(conn net.Conn) context.Context {
		return peer.NewContext(t.Context(), &peer.Peer{Addr: conn.RemoteAddr()})
	}

	require.Equal(t, a.RemoteAddr(), b.RemoteAddr())
	require.Equal(t, a.RemoteAddr().String(), ca.RemoteAddr().String())
	require.Same(t, ca, creds.connOf(callOn(ca)))
	require.Same(t, cb, creds.connOf(callOn(cb)))
	require.Nil(t, creds.connOf(t.Context()), "a context without a peer has no connection")

	require.NoError(t, ca.Close())
	require.Nil(t, creds.connOf(callOn(ca)))
}

func TestServerCreds_AFailedHandshakeServesNoConnection(t *testing.T) {
	creds := &serverCreds{TransportCredentials: credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})}

	conn, client := net.Pipe()
	require.NoError(t, client.Close())

	got, _, err := creds.ServerHandshake(conn)
	require.ErrorIs(t, err, io.EOF, "the client left before the handshake")
	require.Nil(t, got)
}
