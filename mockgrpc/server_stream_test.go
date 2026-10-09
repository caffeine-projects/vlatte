package mockgrpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServerStreaming(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	defer m.Close()
	m.MustStart()

	ctx := context.Background()
	list := []proto.Message{&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}}
	scope := m.MustMock(
		ServerStreamMethod("List").Reply(
			ServerStream[*pb.ListItem]().
				Messages(list)))

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

func TestServerStream_MessageReader(t *testing.T) {
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	defer m.Close()
	m.MustStart()

	buf := strings.Builder{}
	m1 := &pb.ListItem{Key: "\ntest\n"}
	m1b, _ := prototext.Marshal(m1)
	m2 := &pb.ListItem{Key: "\ndev"}
	m2b, _ := prototext.Marshal(m2)

	buf.WriteString(string(m1b))
	buf.WriteString("\n")
	buf.WriteString(string(m2b))

	ctx := context.Background()
	scope := m.MustMock(ServerStreamMethod("List").Reply(
		ServerStream[*pb.ListItem]().
			Text(strings.NewReader(buf.String()))))

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
	require.True(t, proto.Equal(m1, items[0]))
	require.True(t, proto.Equal(m2, items[1]))
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

func TestServerStream_ReaderStreamsOnEveryCall(t *testing.T) {
	// #27: the reply kept the reader and scanned it at each call, so the first call read it to the end, and every later
	// call streamed nothing and still ended with OK. The reply reads the reader once, when it is given.
	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(ServerStreamMethod("List").Reply(
		ServerStream[*pb.ListItem]().JSON(strings.NewReader("{\"key\":\"a\"}\n{\"key\":\"b\"}\n"))))

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewTestServiceClient(conn)
	for i := range 2 {
		require.Equal(t, []string{"a", "b"}, listKeys(t, client), "call %d", i+1)
	}
}
