package mockgrpc

import (
	"context"
	"errors"
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
	"github.com/caffeine-projects/vlatte/descriptor"
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
	// writes it.
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
		ScenarioKey("{{ .Request.Method }}").
		OnCompleteEvent(vlatte.EventConfig{Name: "record", Parameters: map[string]any{"k": "v"}}).
		OnCompleteEvent(vlatte.EventConfig{Name: "record", AfterResponse: true}).
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
		"scenario": {"name": "s", "required_state": "a", "new_state": "b", "key": "{{ .Request.Method }}"},
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

func TestDescribe_APartAMockFileCannotHoldFailsNamingIt(t *testing.T) {
	// A description that left out a part a mock file can't hold showed, in the admin API, a mock that isn't the one
	// serving: one that matches more calls, answers sooner or without its callbacks, or has no reply at all. Describing
	// it fails instead, as a Func or Echo reply does, and the error names the part, wherever it is in the mock.
	custom := match.Function(func(any) (bool, error) { return true, nil })
	fn := Func(func(context.Context, *Call) error { return nil })
	jitter := vlatte.Latency(time.Millisecond)
	reply := Message(&pb.HiRes{Message: "hi"})
	noop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	tests := []struct {
		mock    *Builder
		wantErr error
		wantMsg string
	}{
		{mock: Method("Greetings").ID("func").Reply(fn), wantErr: errFuncNotDescribed},
		{mock: Method("Greetings").ID("echo").Reply(Echo()), wantErr: errEchoNotDescribed},
		{mock: Method("Greetings").ID("shared").Reply(Shared(&countingReplier{})), wantErr: errSharedNotDescribed},
		{mock: Method("Greetings").ID("seq-func").Reply(Seq(reply, fn)), wantErr: errFuncNotDescribed, wantMsg: "responses[1]: "},
		{mock: Method("Greetings").ID("rand-func").Reply(Rand(reply, fn)), wantErr: errFuncNotDescribed},
		{
			mock:    Method("Greetings").ID("seq-after-echo").Reply(Seq(reply).OnSequenceEnded(Echo())),
			wantErr: errEchoNotDescribed,
			wantMsg: "after_ended: ",
		},
		{mock: Method("Greetings").ID("rand-echo").Reply(Rand(Echo())), wantErr: errEchoNotDescribed},
		{mock: Method("Greetings").ID("seq-shared").Reply(Seq(reply, Shared(&countingReplier{}))), wantErr: errSharedNotDescribed},
		{mock: Method("Greetings").ID("rand-shared").Reply(Rand(Shared(&countingReplier{}))), wantErr: errSharedNotDescribed},
		{mock: Method("Greetings").ID("seq-seq").Reply(Seq(Seq(reply))), wantErr: errResponsesOnly},
		{
			mock:    Method("Greetings").ID("call").CallMatches(func(*Call) (bool, error) { return true, nil }).Reply(reply),
			wantErr: errCallMatchesNotDescribed,
		},
		{
			mock:    Method("Greetings").ID("call-func").CallMatches(func(*Call) (bool, error) { return true, nil }).Reply(fn),
			wantErr: errCallMatchesNotDescribed,
		},
		{
			mock:    Method("Greetings").ID("header").Header("x-env", match.Equal("ci")).Header("x-team", custom).Reply(reply),
			wantErr: errMatcherNotDescribed,
			wantMsg: `header "x-team": `,
		},
		{
			mock:    Method("Greetings").ID("field").Field("message", custom).Reply(reply),
			wantErr: errMatcherNotDescribed,
			wantMsg: `fields "message": `,
		},
		{mock: Method("Greetings").ID("body").Body(custom).Reply(reply), wantErr: errMatcherNotDescribed, wantMsg: "body: "},
		{
			mock:    Method("Greetings").ID("custom").Matches(match.Not(match.Nil())).Matches(custom).Reply(reply),
			wantErr: errMatcherNotDescribed,
			wantMsg: "custom[1]: ",
		},
		{
			mock:    Method("Chat").ID("rule").Reply(Bidi().On(custom, Message(&pb.HiRes{}))),
			wantErr: errMatcherNotDescribed,
			wantMsg: "on[0].when: ",
		},
		{mock: Method("Greetings").ID("delay").DelayFunc(jitter).Reply(reply), wantErr: errDelayFuncNotDescribed},
		{mock: Method("Greetings").ID("callback").Callback(noop).Reply(reply), wantErr: errCallbackNotDescribed},
		{
			mock:    Method("Greetings").ID("listener").OnCompleteEvent(vlatte.EventConfig{Name: "audit", Listener: noop}).Reply(reply),
			wantErr: errCallbackNotDescribed,
			wantMsg: `"audit"`,
		},
		{
			mock:    Method("List").ID("message-delay").Reply(Message(&pb.ListItem{}).MessageDelayFunc(jitter)),
			wantErr: errMessageDelayFuncNotDescribed,
		},
		{
			mock:    Method("List").ID("seq-delay").Reply(Seq(Messages(&pb.ListItem{}).MessageDelayFunc(jitter))),
			wantErr: errMessageDelayFuncNotDescribed,
		},
		{
			mock:    Method("Chat").ID("otherwise").Reply(Bidi().Otherwise(Message(&pb.HiRes{}).MessageDelayFunc(jitter))),
			wantErr: errMessageDelayFuncNotDescribed,
			wantMsg: "otherwise: ",
		},
		{
			mock:    Method("Chat").ID("rule-response").Reply(Bidi().On(match.Contain("x"), Message(&pb.HiRes{}).MessageDelayFunc(jitter))),
			wantErr: errMessageDelayFuncNotDescribed,
			wantMsg: "on[0].response: ",
		},
		{mock: Method("Greetings").ID("dial").Reply(From("localhost:1").DialOptions(grpc.WithUserAgent("x"))), wantErr: errDialOptionsNotDescribed},
		{mock: Method("Greetings").ID("own").Reply(&closingReply{}), wantErr: errReplyNotDescribed, wantMsg: "*mockgrpc.closingReply"},
		{
			mock:    Method("Greetings").ID("failing-matcher").Body(failingDescription{}).Reply(reply),
			wantErr: errMatcherNotDescribed,
			wantMsg: "test: no description",
		},
	}

	m := serve(t)

	for _, tt := range tests {
		mock := m.MustMock(tt.mock).Mocks()[0]

		_, err := mock.Describe().MarshalJSON()
		require.ErrorIs(t, err, tt.wantErr, mock.ID())
		require.ErrorContains(t, err, tt.wantMsg, mock.ID())
	}
}

// failingDescription is a matcher that describes itself as something a description can't hold.
type failingDescription struct{}

func (failingDescription) Name() string { return "failing" }

func (failingDescription) Match(vlatte.MatcherArg) (bool, error) { return true, nil }

func (failingDescription) Describe() descriptor.Descriptor {
	return descriptor.Fail("failing", errors.New("test: no description"))
}

func TestDescribe_AMessageAMockFileCannotHoldFailsNamingIt(t *testing.T) {
	// Text that isn't UTF-8, or a status detail of a type the program doesn't have, can't be written as protojson:
	// describing the mock fails with protojson's error, and names the message.
	m := serve(t)
	scope := m.MustMock(
		Method("Greetings").ID("message").Message(&pb.HiReq{Message: "\xff"}).Reply(Message(&pb.HiRes{})),
		Method("Upload").ID("messages").Messages(&pb.ListItem{}, &pb.ListItem{Key: "\xff"}).Reply(Message(&pb.HiRes{})),
		Method("Greetings").ID("reply").Reply(Messages(&pb.HiRes{Message: "\xff"})),
		Method("Greetings").ID("detail").Reply(Status(codes.NotFound, "x").Details(&anypb.Any{TypeUrl: "type.googleapis.com/nope.X"})),
	)

	want := map[string]string{
		"message":  "message: ",
		"messages": "messages[1]: ",
		"reply":    "messages[0]: ",
		"detail":   "status.details[0]: ",
	}

	require.Len(t, scope.Mocks(), len(want))

	for _, mock := range scope.Mocks() {
		_, err := mock.Describe().MarshalJSON()
		require.ErrorContains(t, err, want[mock.ID()], mock.ID())
	}
}

func TestDescribe_AFixedDelayAfterDelayFuncIsDescribed(t *testing.T) {
	// The last delay set wins: Delay after DelayFunc is a delay a mock file holds, and MessageDelay after
	// MessageDelayFunc too.
	m := serve(t)
	scope := m.MustMock(Method("List").ID("fixed").DelayFunc(vlatte.Latency(time.Millisecond)).Delay(time.Second).
		Reply(Messages(&pb.ListItem{Key: "a"}).MessageDelayFunc(vlatte.Latency(time.Millisecond)).MessageDelay(0)))

	doc, err := scope.Mock("fixed").Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"id": "fixed", "delay": "1s", "request": {"method": "List"}, "response": {"messages": [{"key": "a"}]}}`, string(doc))

	_, err = serve(t).Mock(FromBytes(doc, "json"))
	require.NoError(t, err, "%s", doc)
}

func TestDescribe_ACustomMatcherOfTheCallLoadsBack(t *testing.T) {
	// A matcher of Matches that describes itself, such as a custom one of the server's registry, is written under
	// request.custom, so the mock loads back on a server with that registry: a function of CallMatches can't.
	origin := fileServer(t, Setup().Matchers(_customMatchers))
	greets, err := _customMatchers.BuildMatcher([]any{"greets", "ann"})
	require.NoError(t, err)

	scope := origin.MustMock(Method("Greetings").ID("greets").Matches(greets).Reply(Message(&pb.HiRes{Message: "hi ann"})))

	doc, err := scope.Mock("greets").Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"id": "greets", "request": {"method": "Greetings", "custom": [["greets", "ann"]]}, `+
		`"response": {"messages": [{"message": "hi ann"}]}}`, string(doc))

	loaded := fileServer(t, Setup().Matchers(_customMatchers))
	_, err = loaded.Mock(FromBytes(doc, "json"))
	require.NoError(t, err, "%s", doc)

	c := pb.NewTestServiceClient(loaded.MustDial())
	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: "ann"})
	require.NoError(t, err)
	require.Equal(t, "hi ann", res.GetMessage())

	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "bob"})
	require.Equal(t, codes.NotFound, status.Code(err))
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
