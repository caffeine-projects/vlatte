package mockgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestResponse_StatusDetailsReachTheClient(t *testing.T) {
	// Clients read why a call failed from the details of its status, such as the fields a BadRequest names.
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Status(codes.InvalidArgument, "bad name").
		Details(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: "message", Description: "too short"},
		}})))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	st := status.Convert(err)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Equal(t, "bad name", st.Message())
	require.Len(t, st.Details(), 1)

	br, ok := st.Details()[0].(*errdetails.BadRequest)
	require.True(t, ok, "details: %v", st.Details())
	require.Equal(t, "message", br.GetFieldViolations()[0].GetField())
}

func TestResponse_BuildRefusesAnswersTheMethodCantSend(t *testing.T) {
	// Each of these would fail every call, or answer what the client can't read: the mock fails instead.
	m := serve(t)

	tests := []struct {
		name    string
		builder *Builder
		want    string
	}{
		{
			name:    "a message of another type",
			builder: Method("Greetings").Reply(Message(&pb.ListItem{})),
			want:    "message 0 is a protobuf.ListItem, the method answers with protobuf.HiRes",
		},
		{
			name:    "two messages to a unary call",
			builder: Method("Greetings").Reply(Messages(&pb.HiRes{}, &pb.HiRes{})),
			want:    "answers one message, not 2",
		},
		{
			name:    "a message and an error to a unary call",
			builder: Method("Greetings").Reply(Message(&pb.HiRes{}).Status(codes.Internal, "x")),
			want:    "ends with it or with a status, not both",
		},
		{
			name:    "details without an error",
			builder: Method("Greetings").Reply(Message(&pb.HiRes{}).Details(&errdetails.ErrorInfo{})),
			want:    "status details need a status other than OK",
		},
		{
			name:    "JSON of another type",
			builder: Method("Greetings").Reply(JSON(`{"key": "a"}`)),
			want:    "json 0: not a protobuf.HiRes",
		},
		{
			name:    "a template that doesn't compile",
			builder: Method("Greetings").Reply(Template(`{{ .Nope`)),
			want:    "reply: template 0",
		},
		{
			name:    "a bidi reply to a unary method",
			builder: Method("Greetings").Reply(Bidi()),
			want:    "a bidi reply answers bidi streaming methods only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Mock(tt.builder)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResponse_AUnaryAnswerWithoutAMessageIsAnEmptyOne(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Messages()))

	res, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.True(t, proto.Equal(&pb.HiRes{}, res))
}

func TestResponse_AStreamEndsWithItsStatusAfterItsMessages(t *testing.T) {
	// A StreamReply had no status: a stream that fails after some messages couldn't be mocked.
	m := serve(t)
	m.MustMock(Method("List").Reply(
		Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).Status(codes.Aborted, "gone").Trailer("t", "v")))

	var trailer metadata.MD
	stream, err := client(t, m).List(t.Context(), &pb.ListReq{}, grpc.Trailer(&trailer))
	require.NoError(t, err)

	var keys []string
	for {
		item, err := stream.Recv()
		if err != nil {
			require.Equal(t, codes.Aborted, status.Code(err), "the stream ends with the status: %v", err)
			break
		}

		keys = append(keys, item.GetKey())
	}

	require.Equal(t, []string{"a", "b"}, keys)
	require.Equal(t, []string{"v"}, trailer.Get("t"))
}

func TestResponse_AClientThatLeavesBetweenMessagesIsNotCounted(t *testing.T) {
	m := serve(t)
	scope := m.MustMock(Method("List").Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).
		MessageDelayFunc(delays(0, time.Hour))))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stream, err := client(t, m).List(ctx, &pb.ListReq{})
	require.NoError(t, err)

	item, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "a", item.GetKey(), "the first message comes at once")

	cancel()

	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))

	m.Close()
	require.Zero(t, scope.Hits(), "the client got no whole answer")
}

// delays returns a delay that waits each of ds in turn, then the last one.
func delays(ds ...time.Duration) vlatte.Delay {
	next := make(chan time.Duration, len(ds))
	for _, d := range ds {
		next <- d
	}

	last := ds[len(ds)-1]

	return func() time.Duration {
		select {
		case d := <-next:
			return d
		default:
			return last
		}
	}
}

func TestResponse_JSONAnswersInTheMethodsType(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(JSON(`{"message": "from json"}`)))

	got, _ := greetWith(t, client(t, m), "hi")
	require.Equal(t, "from json", got)
}

func TestResponse_TemplateRendersEachCall(t *testing.T) {
	// A template reads the call: its request, as JSON reads it, its header and the server's parameters.
	params := vlatte.NewMemoryKV()
	require.NoError(t, params.Set(t.Context(), "greeting", []byte("hello")))

	m := MustNew(Setup().Silent().Parameters(params))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").Reply(Template(
		`{"message": "{{ .Parameters.Get "greeting" }} {{ .Request.Message.message }} of {{ index .Request.Header "x-team" 0 }}"}`)))

	c := pb.NewTestServiceClient(m.MustDial())
	for _, name := range []string{"ann", "bob"} {
		ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
		res, err := c.Greetings(ctx, &pb.HiReq{Message: name})
		require.NoError(t, err)
		require.Equal(t, "hello "+name+" of core", res.GetMessage())
	}
}

func TestResponse_ATemplateThatRendersNoMessageFailsTheCall(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Template(`{"message": {{ .Request.Message.message }}}`)))

	var trailer metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "not json"}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.FailureHeader))
}

func TestResponse_ADetailOfATypeNoOneKnowsFailsTheMock(t *testing.T) {
	m := serve(t)

	_, err := m.Mock(FromBytes([]byte(`
request: {method: Greetings}
response: {status: {code: NOT_FOUND, details: [{"@type": "type.googleapis.com/nope.Detail"}]}}
`), "yaml"))
	require.ErrorContains(t, err, "reply: status detail 0")
}

func TestResponse_ATemplateThatFailsToRenderFailsTheCall(t *testing.T) {
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Template(`{{ .Request.Nope }}`)))

	var trailer metadata.MD
	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "template 0")
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.FailureHeader))
}

func TestResponse_ADetailThatIsNoMessageFailsTheMock(t *testing.T) {
	_, err := serve(t).Mock(Method("Greetings").Reply(Status(codes.NotFound, "x").Details(&pb.HiReq{Message: "\xff"})))
	require.ErrorContains(t, err, "reply: status detail 0")
}
