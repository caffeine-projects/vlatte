package mockgrpc

import (
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func greetings(n int) []Reply {
	replies := make([]Reply, n)
	for i := range n {
		replies[i] = Message(&pb.HiRes{Message: string(rune('a' + i))})
	}

	return replies
}

func TestRand_AnswersEachCallWithOneOfItsReplies(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("Greetings").Reply(Rand(greetings(2)...).Add(Message(&pb.HiRes{Message: "c"}))))

	seen := make(map[string]bool)
	for range 60 {
		got, _ := greetWith(t, c, "")
		seen[got] = true
	}

	require.Equal(t, map[string]bool{"a": true, "b": true, "c": true}, seen, "60 calls pick each of 3 replies")
}

func TestRandWithSeed_EachMockPicksInTheOrderTheSeedRepeats(t *testing.T) {
	// A flaky dependency a test reproduces: two servers of the same seed answer the same calls the same way.
	b := Method("Greetings").Reply(RandWithSeed(42, greetings(5)...))

	order := func() []string {
		m := serve(t)
		m.MustMock(b)

		got := make([]string, 10)
		for i := range got {
			got[i], _ = greetWith(t, client(t, m), "")
		}

		return got
	}

	first := order()
	require.Equal(t, first, order())
	require.Greater(t, len(uniq(first)), 1, "the order is random, not one reply")
}

func uniq(values []string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, v := range values {
		out[v] = struct{}{}
	}

	return out
}

func TestRandWith_PicksWithTheGivenGenerator(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(RandWith(rand.New(rand.NewPCG(7, 0)), greetings(3)...)))

	same := rand.New(rand.NewPCG(7, 0))
	for range 5 {
		got, _ := greetWith(t, client(t, m), "")
		require.Equal(t, string(rune('a'+same.IntN(3))), got)
	}
}

func TestRand_FailsTheBuildOfAReplyThatDoesntFit(t *testing.T) {
	m := serve(t)

	for _, tc := range []struct {
		name  string
		reply Reply
		want  string
	}{
		{"no reply", Rand(), errReplyRandEmpty.Error()},
		{"a nil reply", Rand(Message(&pb.HiRes{}), nil), "reply_random: response 1 is nil"},
		{"a reply of another type", RandWithSeed(1, Message(&pb.HiRes{}), Message(&pb.ListItem{})), "reply_random: response 1: reply:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(Method("Greetings").Reply(tc.reply))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestRand_ReleasesWhatItsRepliesHold(t *testing.T) {
	held, shared := &closingReply{err: errors.New("busy")}, &closingReply{}

	m := serve(t)
	mock := m.MustMock(Method("Greetings").Reply(Rand(held, Shared(shared)))).Mocks()[0]

	require.ErrorContains(t, mock.Close(), "busy")
	require.Equal(t, int32(1), held.closed.Load())
	require.Zero(t, shared.closed.Load(), "a shared reply is the caller's to close")

	built := &closingReply{}
	_, err := m.Mock(Method("Greetings").Reply(Rand(built, Message(&pb.ListItem{}))))
	require.ErrorContains(t, err, "reply_random: response 1:")
	require.Equal(t, int32(1), built.closed.Load(), "a build that failed releases what it made")
}
