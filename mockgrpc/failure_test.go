package mockgrpc

import (
	"context"
	"errors"
	"iter"
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

// failingStore is a store whose List fails.
type failingStore struct {
	*vlatte.MemoryStore[*Mock]
}

func (failingStore) List(context.Context) (iter.Seq[*Mock], error) {
	return nil, errors.New("store down")
}

// requireMatchFailure asserts err and trailer are the answer to a call the server failed to find a mock for.
func requireMatchFailure(t *testing.T, err error, trailer metadata.MD, msg ...string) {
	t.Helper()

	require.Equal(t, codes.Internal, status.Code(err), "%v", err)
	require.Equal(t, []string{vlatte.FailureMatch}, trailer.Get(vlatte.HeaderFailure))

	for _, m := range msg {
		require.Contains(t, status.Convert(err).Message(), m)
	}
}

func TestFailures_FindingAMock(t *testing.T) {
	// A server that fails to find a mock answers Internal with the match failure, so a test tells it from a missing
	// mock (NotFound, no-match) and from a mock's answer.
	t.Run("a matcher fails", func(t *testing.T) {
		m := serve(t)
		broken := match.Function(func(any) (bool, error) { return false, errors.New("matcher failed") })
		unary := m.MustMock(Method("Greetings").Field("message", broken).Reply(Message(&pb.HiRes{})))
		stream := m.MustMock(Method("List").Field("message", broken).Reply(Messages()))
		c := client(t, m)

		var trailer metadata.MD
		_, err := c.Greetings(context.Background(), &pb.HiReq{Message: "hi"}, grpc.Trailer(&trailer))
		requireMatchFailure(t, err, trailer, "Mock ["+unary.IDs()[0]+"]", "matcher failed")

		s, err := c.List(context.Background(), &pb.ListReq{Message: "hi"})
		require.NoError(t, err)
		_, err = s.Recv()
		requireMatchFailure(t, err, s.Trailer(), "Mock ["+stream.IDs()[0]+"]", "matcher failed")
	})

	t.Run("the store fails", func(t *testing.T) {
		m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
		// Before Start, so serving never races the swap.
		m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}
		m.MustStart()
		t.Cleanup(m.Close)
		c := client(t, m)

		var trailer metadata.MD
		_, err := c.Greetings(context.Background(), &pb.HiReq{}, grpc.Trailer(&trailer))
		requireMatchFailure(t, err, trailer, "store down")

		s, err := c.List(context.Background(), &pb.ListReq{})
		require.NoError(t, err)
		_, err = s.Recv()
		requireMatchFailure(t, err, s.Trailer(), "store down")
	})
}
