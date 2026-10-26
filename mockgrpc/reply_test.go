package mockgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestFunc_AnswersWithWhatItSends(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Greetings").Field("message", match.Equal("fail")).Reply(Func(func(context.Context, *Call) error {
			return status.Error(codes.ResourceExhausted, "slow down")
		})),
		Method("Greetings").Reply(Func(func(_ context.Context, call *Call) error {
			return call.Send(&pb.HiRes{Message: "echo " + call.Message().(*pb.HiReq).GetMessage()})
		})),
		Method("List").Reply(Func(func(_ context.Context, call *Call) error {
			for _, k := range []string{"x", "y"} {
				if err := call.Send(&pb.ListItem{Key: k}); err != nil {
					return err
				}
			}

			return nil
		})),
	)

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "echo hi", got)

	_, code := greetWith(t, c, "fail")
	require.Equal(t, codes.ResourceExhausted, code, "a status is the mock's answer")
	require.Equal(t, []string{"x", "y"}, listKeys(t, c))
}

func TestFunc_ACallThatAnswersOneMessageSendsOnce(t *testing.T) {
	// A unary call has one answer: a second Send fails, rather than replacing the first.
	second := make(chan error, 1)
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Func(func(_ context.Context, call *Call) error {
		if err := call.Send(&pb.HiRes{Message: "first"}); err != nil {
			return err
		}

		second <- call.Send(&pb.HiRes{Message: "second"})

		return nil
	})))

	res, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "first", res.GetMessage())
	require.ErrorIs(t, <-second, errSentTwice)
}
