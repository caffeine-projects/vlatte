package mockgrpc

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// countingReplier answers each call with how many calls it answered.
type countingReplier struct {
	calls atomic.Int64
}

func (r *countingReplier) Reply(_ context.Context, call *Call) error {
	n := r.calls.Add(1)
	return call.Send(&pb.HiRes{Message: string(rune('0' + n))})
}

func TestShared_EveryMockAnswersWithTheSameReplier(t *testing.T) {
	// Two mocks of one replier share what it keeps: here, its count of calls.
	r := &countingReplier{}

	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Shared(r)))

	other := serve(t)
	other.MustMock(Method("Greetings").Reply(Shared(r)))

	got, _ := greetWith(t, client(t, m), "")
	require.Equal(t, "1", got)

	got, _ = greetWith(t, client(t, other), "")
	require.Equal(t, "2", got)
}

func TestShared_TheMocksNeverCloseTheReplier(t *testing.T) {
	r := &closingReply{}

	m := serve(t)
	mock := m.MustMock(Method("Greetings").Reply(Shared(r))).Mocks()[0]

	require.NoError(t, mock.Close())
	m.Close()
	require.Zero(t, r.closed.Load(), "closing it is the caller's")
}

func TestShared_ANilReplierFailsTheBuild(t *testing.T) {
	_, err := serve(t).Mock(Method("Greetings").Reply(Shared(nil)))
	require.ErrorIs(t, err, errNoReplier)
}
