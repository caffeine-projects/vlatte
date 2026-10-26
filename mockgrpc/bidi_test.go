package mockgrpc

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// chat sends each of sent on a Chat stream, reading the answers as they come, closes its side, and returns every
// answer, with the code the call ended with.
func chat(t *testing.T, c pb.TestServiceClient, opts []grpc.CallOption, sent ...string) ([]string, codes.Code) {
	t.Helper()

	stream, err := c.Chat(t.Context(), opts...)
	require.NoError(t, err)

	got := make(chan []string, 1)
	end := make(chan error, 1)

	go func() {
		var answers []string
		for {
			res, err := stream.Recv()
			if err != nil {
				got <- answers
				end <- err
				return
			}

			answers = append(answers, res.GetMessage())
		}
	}()

	for _, msg := range sent {
		if err := stream.Send(&pb.HiReq{Message: msg}); err != nil {
			break // the server ended the call: Recv tells how
		}
	}

	_ = stream.CloseSend()

	answers := <-got
	err = <-end
	if errors.Is(err, io.EOF) {
		err = nil
	}

	return answers, status.Code(err)
}

func TestBidi_AnswersEachMessageByItsRule(t *testing.T) {
	// Bidi streams were served as server streams: one message read, one answer. Each message gets the answer of the
	// first rule that matches it now, and the end of the client's side gets OnEnd's.
	m := serve(t)
	m.MustMock(Method("Chat").Reply(Bidi().
		Header("h", "v").
		Trailer("t", "w").
		On(match.Field("message", match.Equal("hi")), Message(&pb.HiRes{Message: "hello"})).
		On(match.Field("message", match.HasPrefix("list")), Messages(&pb.HiRes{Message: "1"}, &pb.HiRes{Message: "2"})).
		Otherwise(JSON(`{"message": "?"}`)).
		OnEnd(Message(&pb.HiRes{Message: "bye"}))))

	var header, trailer metadata.MD
	got, code := chat(t, client(t, m), []grpc.CallOption{grpc.Header(&header), grpc.Trailer(&trailer)}, "hi", "list all", "what")
	require.Equal(t, codes.OK, code)
	require.Equal(t, []string{"hello", "1", "2", "?", "bye"}, got)
	require.Equal(t, []string{"v"}, header.Get("h"))
	require.Equal(t, []string{"w"}, trailer.Get("t"))
}

func TestBidi_AStatusEndsTheCall(t *testing.T) {
	m := serve(t)
	scope := m.MustMock(Method("Chat").Reply(Bidi().
		On(match.Field("message", match.Equal("quit")), Message(&pb.HiRes{Message: "leaving"}).Status(codes.Aborted, "quit")).
		Otherwise(Message(&pb.HiRes{Message: "ok"}))))

	got, code := chat(t, client(t, m), nil, "a", "quit", "b")
	require.Equal(t, codes.Aborted, code)
	require.Equal(t, []string{"ok", "leaving"}, got, "nothing answers after the status")
	require.Equal(t, int64(1), scope.Hits(), "a status is the mock's answer")
}

func TestBidi_AMessageNoRuleMatchesGetsNoAnswer(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Chat").Reply(Bidi().On(match.Field("message", match.Equal("hi")), Message(&pb.HiRes{Message: "hello"}))))

	got, code := chat(t, client(t, m), nil, "x", "hi", "y")
	require.Equal(t, codes.OK, code)
	require.Equal(t, []string{"hello"}, got)
}

func TestBidi_TheHeaderIsTheBidiReplys(t *testing.T) {
	// A rule's Response answers once the call started: its header would come too late.
	m := serve(t)

	_, err := m.Mock(Method("Chat").Reply(Bidi().Otherwise(Message(&pb.HiRes{}).Header("k", "v"))))
	require.ErrorIs(t, err, errBidiRuleHeaders)
}

func TestBidi_JournalsEveryMessageTheClientSent(t *testing.T) {
	m, c := journaled(t, Setup())
	m.MustMock(Method("Chat").Reply(Bidi()))

	_, code := chat(t, c, nil, "a", "b")
	require.Equal(t, codes.OK, code)

	e := onlyCall(t, m, journal.Query{})
	require.JSONEq(t, `[{"message": "a"}, {"message": "b"}]`, string(e.Body))
}

func TestBidi_BuildRefusesAResponseTheMethodCantSend(t *testing.T) {
	// A rule, otherwise or on_end that can't answer Chat fails the mock when it's added, not the call.
	bad := JSON(`{"nope": 1}`)
	m := serve(t)

	for _, tc := range []struct {
		name  string
		reply *BidiReply
		want  string
	}{
		{"rule", Bidi().On(match.Contain("x"), bad), "reply: rule 0"},
		{"otherwise", Bidi().Otherwise(bad), "reply: otherwise"},
		{"on end", Bidi().OnEnd(bad), "reply: on end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(Method("Chat").Reply(tc.reply))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBidi_ARuleWhoseMatcherFailsFailsTheCall(t *testing.T) {
	// A rule that can't tell whether a message matches ends the call with an error, rather than answering by another.
	broken := match.Function(func(any) (bool, error) { return false, errors.New("broken") })
	m := serve(t)
	m.MustMock(Method("Chat").Reply(Bidi().On(broken, Message(&pb.HiRes{})).Otherwise(Message(&pb.HiRes{Message: "other"}))))

	got, code := chat(t, client(t, m), nil, "a")
	require.Equal(t, codes.Internal, code)
	require.Empty(t, got)
}
