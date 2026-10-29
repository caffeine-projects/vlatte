package mockgrpc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestSeq_AnswersEachCallWithTheNextReplyThenTheOneAfterTheEnd(t *testing.T) {
	// A resource that changes between calls, such as a job that finishes, answers each call in turn.
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("Greetings").Reply(
		Seq(Message(&pb.HiRes{Message: "pending"})).
			Add(Status(codes.Unavailable, "busy")).
			OnSequenceEnded(Message(&pb.HiRes{Message: "done"}))))

	got, code := greetWith(t, c, "")
	require.Equal(t, "pending", got)
	require.Equal(t, codes.OK, code)

	_, code = greetWith(t, c, "")
	require.Equal(t, codes.Unavailable, code, "a status is the mock's answer")

	for range 2 {
		got, _ = greetWith(t, c, "")
		require.Equal(t, "done", got)
	}
}

func TestSeq_AStreamTakesOneReplyWhole(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("List").Reply(Seq(
		Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}),
		Messages(&pb.ListItem{Key: "c"}),
	)))

	require.Equal(t, []string{"a", "b"}, listKeys(t, c))
	require.Equal(t, []string{"c"}, listKeys(t, c))
}

func TestSeq_ACallAfterTheEndWithoutOnSequenceEndedFails(t *testing.T) {
	// A test that calls more often than it planned for gets a failure of the server, not a mock's answer.
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("Greetings").Reply(Seq(Message(&pb.HiRes{Message: "once"}))))

	got, _ := greetWith(t, c, "")
	require.Equal(t, "once", got)

	var trailer metadata.MD
	_, err := c.Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))
	require.Contains(t, status.Convert(err).Message(), "reply_sequence: call 2 came after the end of the sequence: set OnSequenceEnded or after_ended")
}

func TestSeq_EachMockRunsTheSequenceOnItsOwn(t *testing.T) {
	// One builder serves two servers: each starts from the first reply, whatever the other answered.
	b := Method("Greetings").Reply(Seq(Message(&pb.HiRes{Message: "1"}), Message(&pb.HiRes{Message: "2"})))

	one, two := serve(t), serve(t)
	one.MustMock(b)
	two.MustMock(b)

	got, _ := greetWith(t, client(t, one), "")
	require.Equal(t, "1", got)

	got, _ = greetWith(t, client(t, two), "")
	require.Equal(t, "1", got)

	got, _ = greetWith(t, client(t, one), "")
	require.Equal(t, "2", got)
}

func TestSeq_FailsTheBuildOfAReplyThatDoesntFit(t *testing.T) {
	// Each reply of the sequence is checked against the method, as it would be alone, so a wrong one fails the mock
	// rather than a call.
	m := serve(t)

	for _, tc := range []struct {
		name  string
		reply Reply
		want  string
	}{
		{"no reply", Seq(), errReplySeqEmpty.Error()},
		{"a nil reply", Seq(Message(&pb.HiRes{}), nil), "reply_sequence: response 1 is nil"},
		{"a reply of another type", Seq(Message(&pb.HiRes{}), Message(&pb.ListItem{})), "reply_sequence: response 1: reply:"},
		{"after the end of another type", Seq(Message(&pb.HiRes{})).OnSequenceEnded(Message(&pb.ListItem{})), "reply_sequence: after_ended: reply:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(Method("Greetings").Reply(tc.reply))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSeq_ReleasesWhatItsRepliesHold(t *testing.T) {
	// A reply of the sequence may hold a connection, such as a proxy's: the mock releases it when it leaves the server,
	// even when the sequence failed to build. A shared reply is the caller's to close.
	held, after, shared := &closingReply{err: errors.New("busy")}, &closingReply{}, &closingReply{}

	m := serve(t)
	mock := m.MustMock(Method("Greetings").Reply(Seq(held, Shared(shared)).OnSequenceEnded(after))).Mocks()[0]

	require.ErrorContains(t, mock.Close(), "busy")
	require.Equal(t, int32(1), held.closed.Load())
	require.Equal(t, int32(1), after.closed.Load())
	require.Zero(t, shared.closed.Load())

	built := &closingReply{}
	_, err := m.Mock(Method("Greetings").Reply(Seq(built, Message(&pb.ListItem{}))))
	require.ErrorContains(t, err, "reply_sequence: response 1:")
	require.Equal(t, int32(1), built.closed.Load())
}
