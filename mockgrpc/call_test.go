package mockgrpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestCall_RecvIsForBidiCallsOnly(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Func(func(_ context.Context, call *Call) error {
		_, err := call.Recv()
		if !errors.Is(err, errNoRecv) {
			return status.Errorf(codes.Unknown, "recv: %v", err)
		}

		return nil
	})))

	_, code := greetWith(t, client(t, m), "hi")
	require.Equal(t, codes.OK, code)
}

// received is what a reply read of a bidi message: Recv's message, or error, and what Parsed had then.
type received struct {
	text   string
	parsed []any
	err    error
}

// receiving returns a reply that reads one bidi message, sends what it read on got, and answers it.
func receiving(got chan<- received) ReplierFunc {
	return Func(func(_ context.Context, call *Call) error {
		msg, err := call.Recv()
		got <- received{text: envelopeText(msg), parsed: call.Parsed(), err: err}

		return call.Send(&pb.HiRes{})
	})
}

func TestCall_RecvReturnsTheMessageAsSentAndParsedWhatItHolds(t *testing.T) {
	// A reply of its own reads a bidi message as the client sent it with Recv, as a reply that forwards it needs it,
	// and what the parsers read of it with Parsed.
	s := newSealer(t)
	m := parsing(t, opening(s, errors.New("bad envelope")))
	got := make(chan received, 1)
	m.MustMock(Method("Chat").Reply(receiving(got)))

	envelope := s.seal(t, &pb.Rich{UserId: "u1"})
	_, code := chat(t, client(t, m), nil, envelope)
	require.Equal(t, codes.OK, code)

	r := <-got
	require.NoError(t, r.err)
	require.Equal(t, envelope, r.text)
	require.Len(t, r.parsed, 1)
	require.Equal(t, "u1", r.parsed[0].(*pb.Rich).GetUserId())
}

func TestCall_RecvOfAMessageAParserFailsToReadFails(t *testing.T) {
	// A reply tells a message the parsers failed to read by ErrParsing, and why by the parser's error: the call keeps
	// the message, which has no parsed value.
	bad := errors.New("bad envelope")
	m := parsing(t, opening(newSealer(t), bad))
	got := make(chan received, 1)
	m.MustMock(Method("Chat").Reply(receiving(got)))

	_, code := chat(t, client(t, m), nil, "not sealed")
	require.Equal(t, codes.OK, code)

	r := <-got
	require.ErrorIs(t, r.err, ErrParsing)
	require.ErrorIs(t, r.err, bad)
	require.Equal(t, []any{nil}, r.parsed)
}
