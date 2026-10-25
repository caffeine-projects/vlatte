package mockgrpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// journaled starts a gRPC mock server that keeps a journal in a temporary directory, and a client of it.
func journaled(t *testing.T, setup *ConfigBuilder) (*Server, pb.TestServiceClient) {
	t.Helper()

	logger := zerolog.Nop()
	m := MustNew(setup.Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).Logger(&logger).Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return m, pb.NewTestServiceClient(conn)
}

// onlyCall returns the one entry of the journal of m that q selects.
func onlyCall(t *testing.T, m *Server, q journal.Query) *journal.Entry {
	t.Helper()

	entries, err := m.Requests(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	return entries[0]
}

func TestJournal_KeepsEachCallOnceWhateverItsOutcome(t *testing.T) {
	// The journal is the same for every protocol: a gRPC call is an entry, with its full method as its path, its
	// metadata as its headers, its request message as JSON as its body, and its code as its status.
	m, c := journaled(t, Setup())

	m.MustMock(
		UnaryMethod("Greetings").Field("message", match.Equal("hi")).Reply(Unary().Message(&pb.HiRes{Message: "hello"})),
		UnaryMethod("Greetings").Field("message", match.Equal("denied")).Reply(Unary().Status(status.New(codes.PermissionDenied, "no"))),
		UnaryMethod("Greetings").Field("message", match.Equal("broken")).Reply(funcUnaryReply(func() (any, error) {
			return nil, errors.New("unary broke")
		})),
		UnaryMethod("Greetings").Field("message", match.Equal("panics")).Reply(funcUnaryReply(func() (any, error) {
			panic("boom")
		})),
	)

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")

	for _, message := range []string{"hi", "denied", "broken", "panics", "nobody"} {
		_, _ = c.Greetings(ctx, &pb.HiReq{Message: message})
	}

	tests := []struct {
		message string
		outcome journal.Outcome
		code    codes.Code
		mock    bool
	}{
		{message: "hi", outcome: journal.OutcomeMatched, code: codes.OK, mock: true},
		{message: "denied", outcome: journal.OutcomeMatched, code: codes.PermissionDenied, mock: true},
		{message: "broken", outcome: journal.OutcomeFailed, code: codes.Internal, mock: true},
		{message: "panics", outcome: journal.OutcomeFailed, code: codes.Internal, mock: true},
		{message: "nobody", outcome: journal.OutcomeUnmatched, code: codes.NotFound},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			e := onlyCall(t, m, journal.Query{Body: match.Field("message", match.Equal(tt.message))})
			require.Equal(t, tt.outcome, e.Outcome)
			require.Equal(t, int(tt.code), e.Status)
			require.Equal(t, tt.mock, e.MockID != "", e.MockID)
			require.Equal(t, Kind, e.Kind)
			require.Equal(t, "/protobuf.TestService/Greetings", e.Path)
			require.Equal(t, []string{"core"}, e.Header["x-team"])
		})
	}

	ft := testmock.NewFakeT()
	require.True(t, m.AssertRequested(t.Context(), ft, 5, journal.Query{Kind: Kind, Path: match.HasSuffix("/Greetings")}))
	require.True(t, m.AssertRequested(t.Context(), ft, 5, journal.Query{Header: map[string]vlatte.Matcher{"X-Team": match.Equal("core")}}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestJournal_UnmatchedCallsNameTheClosestMocks(t *testing.T) {
	m, c := journaled(t, Setup())
	m.MustMock(UnaryMethod("Greetings").Header("x-team", match.Equal("core")).Reply(Unary().Message(&pb.HiRes{})))

	_, err := c.Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.NotFound, status.Code(err))

	e := onlyCall(t, m, journal.Query{Outcome: journal.OutcomeUnmatched})
	require.Len(t, e.NearMisses, 1)
	require.Equal(t, 1, e.NearMisses[0].Passed)
	require.Equal(t, []string{"header x-team"}, e.NearMisses[0].Failed)
}

func TestJournal_AnUnmatchedCallNamesTheMethodItsClosestMockDiffersIn(t *testing.T) {
	// A mock of another method came closest, the fields of the call matched: the near miss names the method, so a
	// test sees it mocked the wrong method, not the wrong fields.
	m, c := journaled(t, Setup())
	scope := m.MustMock(UnaryMethod("Other").Field("message", match.Equal("hi")).Reply(Unary().Message(&pb.HiRes{})))

	_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, status.Code(err))

	e := onlyCall(t, m, journal.Query{Outcome: journal.OutcomeUnmatched})
	require.Equal(t, []vlatte.NearMiss{{MockID: scope.IDs()[0], Passed: 1, Total: 2, Failed: []string{"method"}}}, e.NearMisses)
}

func TestJournal_KeepsStreamingCalls(t *testing.T) {
	m, c := journaled(t, Setup())
	m.MustMock(ServerStreamMethod("List").Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})))

	stream, err := c.List(t.Context(), &pb.ListReq{Message: "all"})
	require.NoError(t, err)

	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)
	}

	e := onlyCall(t, m, journal.Query{Path: match.Equal("/protobuf.TestService/List")})
	require.Equal(t, journal.OutcomeMatched, e.Outcome)
	require.JSONEq(t, `{"message": "all"}`, string(e.Body))
}

func TestJournal_TheAnswerNamesItsCall(t *testing.T) {
	// As an HTTP answer does, a call's header names its request id, which gets it back from the journal.
	m, c := journaled(t, Setup())

	var header metadata.MD
	_, _ = c.Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))

	ids := header.Get(vlatte.RequestIDHeader)
	require.Len(t, ids, 1)

	e, err := m.Journal().Get(ids[0])
	require.NoError(t, err)
	require.Equal(t, journal.OutcomeUnmatched, e.Outcome)
}

func TestJournal_GRPCHasNoRequestPatterns(t *testing.T) {
	// A gRPC call is verified by its fields: its protocol has no request patterns, and says so.
	m, _ := journaled(t, Setup())

	_, err := m.CountRequests(t.Context(), journal.Query{}, UnaryMethod("Greetings"))
	require.ErrorIs(t, err, journal.ErrNoRequestPatterns)
}

func TestJournal_AServerThatNeverStartedRemovesItsJournal(t *testing.T) {
	m := MustNew(Setup().Journal(""))
	dir := m.Journal().Dir()
	require.DirExists(t, dir)

	m.Close()
	require.NoDirExists(t, dir)
}

func TestJournal_TheAdminAPIServesGRPCCalls(t *testing.T) {
	// The admin API's journal is the same for gRPC: a query of the indexed keys counts calls, and a request part,
	// which gRPC has none of, is a problem of the query.
	logger := zerolog.Nop()
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).Logger(&logger).Admin("").Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, _ = pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{})

	code, body := call(t, "QUERY", m.AdminURL()+"/requests", `{"kind": "grpc", "outcome": "unmatched", "limit": 0}`)
	require.Equal(t, http.StatusOK, code, body)
	require.JSONEq(t, `{"count": 1, "requests": []}`, body)

	code, body = call(t, "QUERY", m.AdminURL()+"/requests", `{"request": {"method": "GET"}}`)
	require.Equal(t, http.StatusBadRequest, code, body)
}

// waitStreamReply is a stream reply that tells it began, then answers once its client leaves, with the status of
// the leaving.
type waitStreamReply struct {
	began chan struct{}
}

func (r waitStreamReply) NewReplier(*Server) (StreamReplier, error) { return r, nil }

func (r waitStreamReply) Reply(ctx context.Context, _ *StreamRequestValues) (any, error) {
	close(r.began)
	<-ctx.Done()

	return nil, status.FromContextError(ctx.Err()).Err()
}

func TestJournal_ACallWhoseClientLeftIsAbandoned(t *testing.T) {
	// A reply that ends as its client leaves is no answer the mock gave: the call is abandoned, of the mock that
	// matched it.
	m, c := journaled(t, Setup())
	reply := waitStreamReply{began: make(chan struct{})}
	m.MustMock(ServerStreamMethod("List").Reply(reply))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err := c.List(ctx, &pb.ListReq{})
	require.NoError(t, err)

	select {
	case <-reply.began:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the mock never replied")
	}

	cancel()

	require.Eventually(t, func() bool {
		n, err := m.CountRequests(t.Context(), journal.Query{})
		return err == nil && n == 1
	}, 5*time.Second, 10*time.Millisecond)

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeAbandoned, e.Outcome)
	require.NotEmpty(t, e.MockID)
}

// greet calls Greetings on c, and returns what failed.
func greet(t *testing.T, c pb.TestServiceClient) error {
	t.Helper()

	_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"})

	return err
}

// list calls List on c, and receives its messages, until the call ends with what failed, if anything.
func list(t *testing.T, c pb.TestServiceClient) error {
	t.Helper()

	stream, err := c.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)

	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}
	}
}

func TestJournal_KeepsTheCodeTheClientGot(t *testing.T) {
	// An interceptor may change the code a mock answered with, as an auth check that runs after it does: the entry has
	// the code the client got, however the interceptor was given, so a test verifies what its client saw.
	deny := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		_, _ = next(ctx, req)
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	unavailable := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		_ = next(srv, ss)
		return status.Error(codes.Unavailable, "unavailable")
	}

	tests := []struct {
		name   string
		option grpc.ServerOption
		call   func(t *testing.T, c pb.TestServiceClient) error
		want   codes.Code
	}{
		{name: "a unary interceptor of its own", option: grpc.UnaryInterceptor(deny), call: greet, want: codes.PermissionDenied},
		{name: "a chained unary interceptor", option: grpc.ChainUnaryInterceptor(deny), call: greet, want: codes.PermissionDenied},
		{name: "a chained stream interceptor", option: grpc.ChainStreamInterceptor(unavailable), call: list, want: codes.Unavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, c := journaled(t, Setup().ServerOptions(tt.option))
			m.MustMock(
				UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{})),
				ServerStreamMethod("List").Reply(ServerStream[*pb.ListItem]().Messages([]proto.Message{&pb.ListItem{Key: "a"}})),
			)

			require.Equal(t, tt.want, status.Code(tt.call(t, c)))

			e := onlyCall(t, m, journal.Query{})
			require.Equal(t, int(tt.want), e.Status, "the code the client got")
			require.Equal(t, journal.OutcomeMatched, e.Outcome)
		})
	}
}

func TestJournal_LeavesOutTheCallsAnInterceptorAnswers(t *testing.T) {
	// An interceptor that answers a call itself, as one that refuses it does, answers it before any mock: the call is
	// none of the journal's, however the interceptor was given.
	refuse := func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
		return nil, status.Error(codes.Unauthenticated, "who are you")
	}
	refuseStream := func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error {
		return status.Error(codes.Unauthenticated, "who are you")
	}

	tests := []struct {
		name   string
		option grpc.ServerOption
		call   func(t *testing.T, c pb.TestServiceClient) error
	}{
		{name: "a unary interceptor of its own", option: grpc.UnaryInterceptor(refuse), call: greet},
		{name: "a chained unary interceptor", option: grpc.ChainUnaryInterceptor(refuse), call: greet},
		{name: "a chained stream interceptor", option: grpc.ChainStreamInterceptor(refuseStream), call: list},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, c := journaled(t, Setup().ServerOptions(tt.option))
			m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{})))

			require.Equal(t, codes.Unauthenticated, status.Code(tt.call(t, c)))

			n, err := m.CountRequests(t.Context(), journal.Query{})
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestJournal_KeepsOneEntryOfACallAnInterceptorHidesFromIt(t *testing.T) {
	// An interceptor that hands the next handler a context of its own, not the call's, hides the call from the mocks,
	// whose reply then fails: the call is journaled once all the same, with the code the client got, not twice.
	detach := func(_ context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		return next(context.Background(), req)
	}

	m, c := journaled(t, Setup().ServerOptions(grpc.ChainUnaryInterceptor(detach)))
	m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{})))

	require.Equal(t, codes.Internal, status.Code(greet(t, c)))
	require.Equal(t, int(codes.Internal), onlyCall(t, m, journal.Query{}).Status)
}

// rawCodec sends a request message as the bytes it is, which may be no message at all.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) { return v.([]byte), nil }
func (rawCodec) Unmarshal([]byte, any) error   { return nil }
func (rawCodec) Name() string                  { return "proto" }

func TestJournal_LeavesOutACallWhoseRequestDoesntDecode(t *testing.T) {
	// grpc answers a request it can't decode itself, before any interceptor or mock, as it does a call of an unknown
	// method: the call is none of the journal's, which couldn't append its entry before the client had its answer.
	m, _ := journaled(t, Setup())

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// A field of 255 bytes, of which none came.
	err = conn.Invoke(t.Context(), pb.TestService_Greetings_FullMethodName, []byte{0x0a, 0xff}, &pb.HiRes{}, grpc.ForceCodec(rawCodec{}))
	require.Equal(t, codes.Internal, status.Code(err))

	n, err := m.CountRequests(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestJournal_KeepsACallAnInterceptorAnswersWhileItsMockGoesOn(t *testing.T) {
	// An interceptor may answer while the mock goes on, as one that times it out runs it apart: the entry has the
	// code the client got, whatever the mock does as the journal appends it. make test runs with -race, which fails
	// this test when the mock and the journal share the entry unguarded.
	started, done := make(chan struct{}), make(chan struct{})
	timeout := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		go func() {
			defer close(done)

			_, _ = next(ctx, req)
		}()

		<-started

		return nil, status.Error(codes.DeadlineExceeded, "too slow")
	}

	m, c := journaled(t, Setup().ServerOptions(grpc.ChainUnaryInterceptor(timeout)))
	m.MustMock(UnaryMethod("Greetings").Reply(funcUnaryReply(func() (any, error) {
		close(started)
		return &pb.HiRes{}, nil
	})))

	require.Equal(t, codes.DeadlineExceeded, status.Code(greet(t, c)))
	<-done

	require.Equal(t, int(codes.DeadlineExceeded), onlyCall(t, m, journal.Query{}).Status)
}
