package mockgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestDescribe_AMockBuiltInGoLoadsBackTheSame(t *testing.T) {
	// The admin API returns and saves mocks as their descriptions: one built in Go, of what mock files hold, loads
	// back as a mock that answers the same.
	builders := []vlatte.Builder[*Mock, *Server]{
		Method("Greetings").ID("a").Header("x-team", match.Equal("core")).JSON(`{"message": "a"}`).
			Reply(Message(&pb.HiRes{Message: "for a"}).Header("h", "1")),
		Method("Greetings").ID("b").Priority(1).Message(&pb.HiReq{Message: "b"}).
			Reply(Status(codes.NotFound, "no b").Details(&errdetails.ErrorInfo{Reason: "B"})),
		Method("List").ID("c").Reply(JSON(`{"key": "x"}`, `{"key": "y"}`).Status(codes.Aborted, "cut")),
	}

	origin := fileServer(t, Setup())
	scope := origin.MustMock(builders...)

	loaded := fileServer(t, Setup())

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)

		_, err = loaded.Mock(FromBytes(doc, "json"))
		require.NoError(t, err, "%s", doc)
	}

	for _, m := range []*Server{origin, loaded} {
		c := pb.NewTestServiceClient(m.MustDial())

		ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
		res, err := c.Greetings(ctx, &pb.HiReq{Message: "a"})
		require.NoError(t, err)
		require.Equal(t, "for a", res.GetMessage())

		_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "b"})
		st := status.Convert(err)
		require.Equal(t, codes.NotFound, st.Code())
		require.Len(t, st.Details(), 1)

		stream, err := c.List(context.WithoutCancel(t.Context()), &pb.ListReq{})
		require.NoError(t, err)

		item, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "x", item.GetKey())
	}
}

func TestDescribe_WritesEveryPartAMockFileHolds(t *testing.T) {
	// The admin API shows a mock built in Go as its description: each part a mock file holds is written as the file
	// writes it, and the parts it can't hold, such as a callback, are left out.
	noop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })
	m := MustNew(Setup().Silent().OnCompleteListener("record", noop))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Method("List").
		ID("full").
		Name("greet").
		Priority(2).
		Enabled(false).
		Delay(time.Second).
		Times(3).
		ScenarioIs("s").
		ScenarioStateIs("a").
		ScenarioStateWillBe("b").
		OnCompleteEvent(vlatte.EventConfig{Name: "record", Parameters: map[string]any{"k": "v"}}).
		Callback(noop).
		Field("message", match.Equal("hi")).
		Body(match.Contain("hi")).
		Reply(Template(`{"message": "{{ .Request.message }}"}`).
			Trailer("T", "1").
			Status(codes.Aborted, "cut").
			MessageDelay(time.Millisecond)))

	doc, err := scope.Mocks()[0].Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "full",
		"name": "greet",
		"priority": 2,
		"enabled": false,
		"delay": "1s",
		"repeat": 3,
		"scenario": {"name": "s", "required_state": "a", "new_state": "b"},
		"on_complete": [{"name": "record", "parameters": {"k": "v"}}],
		"request": {
			"method": "List",
			"fields": {"message": ["eq", "hi"]},
			"body": ["contains", "hi"]
		},
		"response": {
			"messages": ["{\"message\": \"{{ .Request.message }}\"}"],
			"template": true,
			"trailer": {"t": ["1"]},
			"status": {"code": "ABORTED", "message": "cut"},
			"message_delay": "1ms"
		}
	}`, string(doc))
}

func TestDescribe_WritesBidiRulesAndStreamedMessages(t *testing.T) {
	m := serve(t)
	scope := m.MustMock(
		Method("Chat").ID("chat").Reply(Bidi().
			Header("h", "1").
			Trailer("t", "2").
			On(match.Contain("hi"), Message(&pb.HiRes{Message: "hello"})).
			Otherwise(JSON(`{"message": "what"}`)).
			OnEnd(Messages(&pb.HiRes{Message: "bye"}))),
		Method("Upload").ID("upload").JSON(`[{"key": "a"}]`).
			Reply(Status(codes.InvalidArgument, "bad").Details(&errdetails.ErrorInfo{Reason: "R"})),
		Method("Upload").ID("messages").Messages(&pb.ListItem{Key: "b"}).Reply(Message(&pb.HiRes{})),
	)

	want := map[string]string{
		"chat": `{
			"id": "chat",
			"request": {"method": "Chat"},
			"bidi": {
				"header": {"h": ["1"]},
				"trailer": {"t": ["2"]},
				"on": [{"when": ["contains", "hi"], "response": {"messages": [{"message": "hello"}]}}],
				"otherwise": {"messages": [{"message": "what"}]},
				"on_end": {"messages": [{"message": "bye"}]}
			}
		}`,
		"upload": `{
			"id": "upload",
			"request": {"method": "Upload", "messages": [{"key": "a"}]},
			"response": {"status": {"code": "INVALID_ARGUMENT", "message": "bad", "details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "R"}
			]}}
		}`,
		"messages": `{
			"id": "messages",
			"request": {"method": "Upload", "messages": [{"key": "b"}]},
			"response": {"messages": [{}]}
		}`,
	}

	require.Len(t, scope.Mocks(), len(want))

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))
	}
}

func TestDescribe_LeavesOutAReplyAMockFileCannotHold(t *testing.T) {
	// A description without a response doesn't load back: the admin API shows what it can, rather than a reply that
	// answers differently.
	custom := match.Function(func(any) (bool, error) { return true, nil })
	m := serve(t)
	scope := m.MustMock(
		Method("List").ID("delay").Reply(Message(&pb.ListItem{}).MessageDelayFunc(vlatte.Latency(time.Millisecond))),
		Method("Chat").ID("rule").Reply(Bidi().On(custom, Message(&pb.HiRes{}))),
		Method("Chat").ID("otherwise").Reply(Bidi().Otherwise(Message(&pb.HiRes{}).MessageDelayFunc(vlatte.Latency(0)))),
		Method("Chat").ID("rule-response").
			Reply(Bidi().On(match.Contain("x"), Message(&pb.HiRes{}).MessageDelayFunc(vlatte.Latency(0)))),
		Method("Greetings").ID("call").CallMatches(func(*Call) (bool, error) { return true, nil }).Body(custom).
			Reply(Func(func(context.Context, *Call) error { return nil })),
	)

	want := map[string]string{
		"delay":         `{"id": "delay", "request": {"method": "List"}}`,
		"rule":          `{"id": "rule", "request": {"method": "Chat"}}`,
		"otherwise":     `{"id": "otherwise", "request": {"method": "Chat"}}`,
		"rule-response": `{"id": "rule-response", "request": {"method": "Chat"}}`,
		"call":          `{"id": "call", "request": {"method": "Greetings"}}`,
	}

	require.Len(t, scope.Mocks(), len(want))

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))
	}
}
