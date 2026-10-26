package mockgrpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServerStreaming(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	defer m.Close()
	m.MustStart()

	ctx := context.Background()
	list := []proto.Message{&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}}
	scope := m.MustMock(Method("List").Reply(Messages(list...)))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)
	res, err := client.List(ctx, &pb.ListReq{})

	require.NoError(t, err)
	require.NotNil(t, res)

	mu := sync.Mutex{}
	items := make([]*pb.ListItem, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for {
			item, err := res.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					cancel()
					return
				}

				assert.Fail(t, err.Error())
				cancel()
				return
			}

			mu.Lock()
			items = append(items, item)
			mu.Unlock()
		}
	}()

	<-ctx.Done()

	require.Len(t, items, 2)
	require.Equal(t, items[0].String(), list[0].(*pb.ListItem).String())
	require.Equal(t, items[1].String(), list[1].(*pb.ListItem).String())
	require.True(t, scope.AssertCalled(t))
}

// listKeys calls List and returns the keys of the items it streams, in order.
func listKeys(t *testing.T, client pb.TestServiceClient) []string {
	t.Helper()

	stream, err := client.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)

	keys := make([]string, 0)
	for {
		item, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return keys
		}

		require.NoError(t, err)
		keys = append(keys, item.Key)
	}
}

func TestServerStream_JSONStreamsOnEveryCall(t *testing.T) {
	// #27: the reply kept the reader of its JSON messages and scanned it at each call, so the first call read it to the
	// end, and every later call streamed nothing and still ended with OK. The reply reads its JSON once, as it builds.
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("List").Reply(
		JSON(`{"key":"a"}`, `{"key":"b"}`)))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)
	for i := range 2 {
		require.Equal(t, []string{"a", "b"}, listKeys(t, client), "call %d", i+1)
	}
}

func TestServerStream_AnUnmatchedCallNamesTheClosestMocks(t *testing.T) {
	// A streaming call no mock matched is journaled as a unary one is: with the mocks that came closest, and the part
	// each differs in, which tell a test what its mock got wrong.
	m, c := journaled(t, Setup())
	scope := m.MustMock(Method("List").Field("message", match.Equal("all")).Reply(Messages()))

	stream, err := c.List(t.Context(), &pb.ListReq{Message: "some"})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err))

	e := onlyCall(t, m, journal.Query{Outcome: journal.OutcomeUnmatched})
	require.Equal(t, []vlatte.NearMiss{{MockID: scope.IDs()[0], Passed: 1, Total: 2, Failed: []string{"field message"}}}, e.NearMisses)
}

// upload sends keys on a client stream of Upload, and returns the answer, or the code it failed with.
func upload(t *testing.T, c pb.TestServiceClient, keys ...string) (string, codes.Code) {
	t.Helper()

	stream, err := c.Upload(t.Context())
	require.NoError(t, err)

	for _, k := range keys {
		require.NoError(t, stream.Send(&pb.ListItem{Key: k}))
	}

	res, err := stream.CloseAndRecv()

	return res.GetMessage(), status.Code(err)
}

func TestClientStream_MatchesTheMessagesTheClientSent(t *testing.T) {
	// Client streams were served as server streams: the mock read one message, matched it, and answered before the
	// client was done. The call is matched once the client closed its side, on all of its messages.
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Upload").Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).Reply(Message(&pb.HiRes{Message: "a, b"})),
		Method("Upload").Field("$[0].key", match.Equal("x")).Reply(Template(`{"message": "{{ len .Request.Message }} from x"}`)),
	)

	got, code := upload(t, c, "a", "b")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "a, b", got)

	got, _ = upload(t, c, "x", "y", "z")
	require.Equal(t, "3 from x", got)

	_, code = upload(t, c, "a")
	require.Equal(t, codes.NotFound, code)
}

func TestClientStream_JournalsEveryMessage(t *testing.T) {
	m, c := journaled(t, Setup())
	m.MustMock(Method("Upload").Reply(Message(&pb.HiRes{})))

	_, code := upload(t, c, "a", "b")
	require.Equal(t, codes.OK, code)

	e := onlyCall(t, m, journal.Query{})
	require.JSONEq(t, `[{"key": "a"}, {"key": "b"}]`, string(e.Body))
}
