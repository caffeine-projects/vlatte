package mockgrpc

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

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
		OnCompleteEvent(vlatte.EventConfig{Name: "record", AfterResponse: true}).
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
		"on_complete": [{"name": "record", "parameters": {"k": "v"}}, {"name": "record", "after_response": true}],
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
		Method("Chat").ID("rules-only").Reply(Bidi().On(match.Contain("x"), Message(&pb.HiRes{}))),
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
		"rules-only": `{
			"id": "rules-only",
			"request": {"method": "Chat"},
			"bidi": {"on": [{"when": ["contains", "x"], "response": {"messages": [{}]}}]}
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

func TestDescribe_WritesAProxy(t *testing.T) {
	// A proxy built in Go is described as a mock file holds it, and loads back as the same proxy.
	m := serve(t)
	scope := m.MustMock(
		Method("Greetings").ID("full").Reply(From("localhost:1").Header("X-Token", "a", "b").RemoveHeaders("x-drop")),
		Method("Greetings").ID("bare").Reply(From("localhost:2")),
	)

	want := map[string]string{
		"full": `{"id": "full", "request": {"method": "Greetings"}, "proxy": {"target": "localhost:1", ` +
			`"header": {"x-token": ["a", "b"]}, "remove_headers": ["x-drop"]}}`,
		"bare": `{"id": "bare", "request": {"method": "Greetings"}, "proxy": {"target": "localhost:2"}}`,
	}

	loaded := serve(t)

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))

		again, err := loaded.Mock(FromBytes(doc, "json"))
		require.NoError(t, err)

		redoc, err := again.Mocks()[0].Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(redoc))
	}
}

func TestDescribe_WritesSequencesAndRandoms(t *testing.T) {
	// A sequence or random reply of Responses is described as a mock file holds it, and loads back as the same.
	m := serve(t)
	scope := m.MustMock(
		Method("Greetings").ID("seq").Reply(Seq(Message(&pb.HiRes{Message: "a"}), Status(codes.NotFound, "b")).
			OnSequenceEnded(JSON(`{"message": "c"}`))),
		Method("Greetings").ID("seeded").Reply(RandWithSeed(3, Message(&pb.HiRes{Message: "a"}))),
		Method("Greetings").ID("generator").Reply(RandWith(rand.New(rand.NewPCG(1, 2)), Message(&pb.HiRes{Message: "a"}))),
	)

	want := map[string]string{
		"seq": `{"id": "seq", "request": {"method": "Greetings"}, "response_sequence": {"responses": [` +
			`{"messages": [{"message": "a"}]}, {"status": {"code": "NOT_FOUND", "message": "b"}}], ` +
			`"after_ended": {"messages": [{"message": "c"}]}}}`,
		"seeded":    `{"id": "seeded", "request": {"method": "Greetings"}, "response_random": {"responses": [{"messages": [{"message": "a"}]}], "seed": 3}}`,
		"generator": `{"id": "generator", "request": {"method": "Greetings"}, "response_random": {"responses": [{"messages": [{"message": "a"}]}]}}`,
	}

	var seq *Server

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))

		loaded := serve(t)
		_, err = loaded.Mock(FromBytes(doc, "json"))
		require.NoError(t, err, "%s", doc)

		if mock.ID() == "seq" {
			seq = loaded
		}
	}

	got := make([]string, 0, 3)
	for range 3 {
		greeting, code := greetWith(t, client(t, seq), "")
		got = append(got, greeting+code.String())
	}

	require.Equal(t, []string{"aOK", "NotFound", "cOK"}, got, "the sequence loaded back answers in turn")
}

func TestDescribe_LeavesOutAReplyAMockFileCannotHold(t *testing.T) {
	// A description without a response doesn't load back: the admin API shows what it can, rather than a reply that
	// answers differently.
	custom := match.Function(func(any) (bool, error) { return true, nil })
	m := serve(t)
	scope := m.MustMock(
		Method("List").ID("delay").Reply(Message(&pb.ListItem{}).MessageDelayFunc(vlatte.Latency(time.Millisecond))),
		Method("Chat").ID("rule").Reply(Bidi().On(custom, Message(&pb.HiRes{}))),
		Method("Chat").ID("otherwise").Reply(Bidi().Otherwise(Message(&pb.HiRes{}).MessageDelayFunc(vlatte.Latency(time.Millisecond)))),
		Method("Chat").ID("rule-response").
			Reply(Bidi().On(match.Contain("x"), Message(&pb.HiRes{}).MessageDelayFunc(vlatte.Latency(time.Millisecond)))),
		Method("Greetings").ID("call").CallMatches(func(*Call) (bool, error) { return true, nil }).Body(custom).
			Reply(Func(func(context.Context, *Call) error { return nil })),
		Method("Greetings").ID("utf8").Message(&pb.HiReq{Message: "\xff"}).Reply(Messages(&pb.HiRes{Message: "\xff"})),
		Method("Greetings").ID("detail").Reply(Status(codes.NotFound, "x").Details(&anypb.Any{TypeUrl: "type.googleapis.com/nope.X"})),
		Method("Greetings").ID("dial").Reply(From("localhost:1").DialOptions(grpc.WithUserAgent("x"))),
		Method("Greetings").ID("echo").Reply(Echo()),
		Method("Greetings").ID("shared").Reply(Shared(&countingReplier{})),
		Method("Greetings").ID("seq-func").Reply(Seq(Message(&pb.HiRes{}), Func(func(context.Context, *Call) error { return nil }))),
		Method("Greetings").ID("seq-after").Reply(Seq(Message(&pb.HiRes{})).OnSequenceEnded(Echo())),
		Method("List").ID("seq-delay").Reply(Seq(Messages(&pb.ListItem{}).MessageDelayFunc(vlatte.Latency(time.Millisecond)))),
		Method("Greetings").ID("rand-echo").Reply(Rand(Echo())),
	)

	want := map[string]string{
		"delay":         `{"id": "delay", "request": {"method": "List"}}`,
		"rule":          `{"id": "rule", "request": {"method": "Chat"}}`,
		"otherwise":     `{"id": "otherwise", "request": {"method": "Chat"}}`,
		"rule-response": `{"id": "rule-response", "request": {"method": "Chat"}}`,
		"call":          `{"id": "call", "request": {"method": "Greetings"}}`,
		"utf8":          `{"id": "utf8", "request": {"method": "Greetings"}}`,
		"detail":        `{"id": "detail", "request": {"method": "Greetings"}}`,
		"dial":          `{"id": "dial", "request": {"method": "Greetings"}}`,
		"echo":          `{"id": "echo", "request": {"method": "Greetings"}}`,
		"shared":        `{"id": "shared", "request": {"method": "Greetings"}}`,
		"seq-func":      `{"id": "seq-func", "request": {"method": "Greetings"}}`,
		"seq-after":     `{"id": "seq-after", "request": {"method": "Greetings"}}`,
		"seq-delay":     `{"id": "seq-delay", "request": {"method": "List"}}`,
		"rand-echo":     `{"id": "rand-echo", "request": {"method": "Greetings"}}`,
	}

	require.Len(t, scope.Mocks(), len(want))

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))
	}
}

func TestDescribe_AMockWithAMatcherOrDelayItCannotHoldHasNoResponse(t *testing.T) {
	// A description without a matcher, or the delay, of its mock would load back as a mock that matches more calls,
	// or answers sooner: it has no response, so it doesn't load back at all.
	custom := match.Function(func(any) (bool, error) { return true, nil })
	reply := Message(&pb.HiRes{Message: "hi"})
	m := serve(t)
	scope := m.MustMock(
		Method("Greetings").ID("call").CallMatches(func(*Call) (bool, error) { return true, nil }).Reply(reply),
		Method("Greetings").ID("header").Header("x-team", custom).Header("x-env", match.Equal("ci")).Reply(reply),
		Method("Greetings").ID("field").Field("message", custom).Reply(reply),
		Method("Greetings").ID("body").Body(custom).Reply(reply),
		Method("Greetings").ID("message").Message(&pb.HiReq{Message: "\xff"}).Reply(reply),
		Method("Upload").ID("messages").Messages(&pb.ListItem{Key: "\xff"}).Reply(reply),
		Method("Greetings").ID("delay").DelayFunc(vlatte.Latency(time.Millisecond)).Reply(reply),
		Method("Greetings").ID("delay-then-fixed").DelayFunc(vlatte.Latency(time.Millisecond)).Delay(time.Second).Reply(reply),
	)

	want := map[string]string{
		"call":             `{"id": "call", "request": {"method": "Greetings"}}`,
		"header":           `{"id": "header", "request": {"method": "Greetings", "header": {"x-env": ["eq", "ci"]}}}`,
		"field":            `{"id": "field", "request": {"method": "Greetings"}}`,
		"body":             `{"id": "body", "request": {"method": "Greetings"}}`,
		"message":          `{"id": "message", "request": {"method": "Greetings"}}`,
		"messages":         `{"id": "messages", "request": {"method": "Upload"}}`,
		"delay":            `{"id": "delay", "request": {"method": "Greetings"}}`,
		"delay-then-fixed": `{"id": "delay-then-fixed", "delay": "1s", "request": {"method": "Greetings"}, "response": {"messages": [{"message": "hi"}]}}`,
	}

	require.Len(t, scope.Mocks(), len(want))

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, want[mock.ID()], string(doc))

		if mock.ID() != "delay-then-fixed" {
			_, err = serve(t).Mock(FromBytes(doc, "json"))
			require.ErrorContains(t, err, "the mock has no response", mock.ID())
		}
	}
}

func TestDescribe_AMockOfADocumentSaysWhetherItIsEnabled(t *testing.T) {
	// The admin API turns a mock off and on without changing its document: the description says what it is now.
	m := serve(t)
	scope := m.MustMock(FromBytes([]byte(`{"id": "doc", "enabled": false, "request": {"method": "Greetings"}, "response": {}}`), "json"))
	mock := scope.Mock("doc")

	doc, err := mock.Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"id": "doc", "enabled": false, "request": {"method": "Greetings"}, "response": {}}`, string(doc))

	mock.Enable()

	doc, err = mock.Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"id": "doc", "request": {"method": "Greetings"}, "response": {}}`, string(doc))
}
