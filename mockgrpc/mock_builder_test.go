package mockgrpc

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mockid"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// greetWith calls Greetings with message and returns the message of the answer, or the code it failed with.
func greetWith(t *testing.T, c pb.TestServiceClient, message string) (string, codes.Code) {
	t.Helper()

	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: message})

	return res.GetMessage(), status.Code(err)
}

func TestBuilder_IDNamesTheMock(t *testing.T) {
	// gRPC mocks had generated IDs only, so a test or the admin API couldn't name one.
	m := serve(t)

	scope := m.MustMock(Method("Greetings").ID("greet-hi").Name("greet").Reply(Message(&pb.HiRes{})))
	require.Equal(t, []string{"greet-hi"}, scope.IDs())
	require.Equal(t, "greet", scope.Mock("greet-hi").Name())

	_, err := m.Mock(Method("Greetings").ID("greet hi").Reply(Message(&pb.HiRes{})))
	require.ErrorIs(t, err, mockid.ErrInvalid)

	_, err = m.Mock(Method("Greetings").ID("greet-hi").Reply(Message(&pb.HiRes{})))
	require.ErrorIs(t, err, vlatte.ErrDuplicateID, "a server holds one mock per ID")
}

func TestBuilder_APriorityMockMatchesFirst(t *testing.T) {
	// Mocks of equal priority match in the order they were added: a lower priority goes first, whenever it was added.
	m := serve(t)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "added first"})))
	m.MustMock(Method("Greetings").Priority(-1).Reply(Message(&pb.HiRes{Message: "priority"})))

	got, _ := greetWith(t, client(t, m), "hi")
	require.Equal(t, "priority", got)
}

func TestBuilder_ADisabledMockMatchesNothing(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	scope := m.MustMock(Method("Greetings").Enabled(false).Reply(Message(&pb.HiRes{Message: "bye"})))

	_, code := greetWith(t, c, "hi")
	require.Equal(t, codes.NotFound, code)

	scope.Enable()

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "bye", got)
}

func TestBuilder_TimesServesThatManyCalls(t *testing.T) {
	// Each mock counts its own calls, a status answer included: once it served them, the next mock answers.
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Greetings").Field("message", match.Equal("fail")).Once().Reply(Status(codes.Unavailable, "down")),
		Method("Greetings").Times(2).Reply(Message(&pb.HiRes{Message: "limited"})),
		Method("Greetings").Reply(Message(&pb.HiRes{Message: "fallback"})),
	)

	_, code := greetWith(t, c, "fail")
	require.Equal(t, codes.Unavailable, code)

	got := make([]string, 0, 4)
	for _, msg := range []string{"fail", "hi", "hi", "hi"} {
		res, _ := greetWith(t, c, msg)
		got = append(got, res)
	}

	require.Equal(t, []string{"limited", "limited", "fallback", "fallback"}, got)

	_, err := m.Mock(Method("Greetings").Times(0).Reply(Message(&pb.HiRes{})))
	require.ErrorContains(t, err, "times 0: expected 1 or more")
}

func TestBuilder_AScenarioMovesOnAsItsMocksAnswer(t *testing.T) {
	// The state moved by nothing: a gRPC mock never set its stateful matchers' state, so a scenario stayed in its
	// first state. A capitalized name works as a lowercase one.
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Greetings").StartScenario("Checkout").ScenarioStateWillBe("paid").
			Reply(Message(&pb.HiRes{Message: "pay"})),
		Method("Greetings").ScenarioIs("Checkout").ScenarioStateIs("paid").ScenarioStateWillBe("refused").
			Reply(Status(codes.FailedPrecondition, "paid already")),
		Method("Greetings").ScenarioIs("Checkout").ScenarioStateIs("refused").
			Reply(Message(&pb.HiRes{Message: "done"})),
	)

	got, _ := greetWith(t, c, "")
	require.Equal(t, "pay", got)

	_, code := greetWith(t, c, "")
	require.Equal(t, codes.FailedPrecondition, code, "a status answer moves the scenario too")

	got, _ = greetWith(t, c, "")
	require.Equal(t, "done", got)
	require.Equal(t, map[string]string{"Checkout": "refused"}, m.scenarios.Scenarios())
}

func TestBuilder_AClientThatLeavesDuringTheDelayIsNotCounted(t *testing.T) {
	m := serve(t)
	waiting := make(chan struct{})
	scope := m.MustMock(Method("Greetings").Once().
		DelayFunc(func() time.Duration {
			close(waiting)
			return time.Hour
		}).
		Reply(Message(&pb.HiRes{})))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	called := make(chan error, 1)
	go func() {
		_, err := client(t, m).Greetings(ctx, &pb.HiReq{})
		called <- err
	}()

	<-waiting
	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-called))

	// Close waits for the call: the server saw the client leave.
	m.Close()
	require.Zero(t, scope.Hits(), "nothing answered the call")
}

func TestBuilder_ACallbackSeesTheCallItsMockAnswered(t *testing.T) {
	// The call ends after its callbacks: a test reads what one saw right after the call, on its own goroutine.
	m := serve(t)
	seen := make(chan *OnComplete, 1)
	m.MustMock(Method("Greetings").ID("greet").
		Callback(func(_ context.Context, e vlatte.OnComplete) error {
			seen <- e.(*OnComplete)
			return nil
		}).
		Reply(Message(&pb.HiRes{Message: "bye"})))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
	_, err := client(t, m).Greetings(ctx, &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	e := <-seen
	require.Equal(t, Kind, e.Kind())
	require.Equal(t, "greet", e.Mock().ID())
	require.Equal(t, pb.TestService_Greetings_FullMethodName, e.Call().Method())
	require.Equal(t, []string{"core"}, e.Call().Header().Get("x-team"))
	require.True(t, proto.Equal(&pb.HiReq{Message: "hi"}, e.Call().Message()))
	require.Len(t, e.Call().Sent(), 1)
	require.True(t, proto.Equal(&pb.HiRes{Message: "bye"}, e.Call().Sent()[0]))
	require.Equal(t, codes.OK, e.Status().Code())
}

func TestBuilder_OnCompleteEventsRunTheirRegisteredListeners(t *testing.T) {
	// gRPC mocks ran no events: a mock file's webhook never fired.
	got := make(chan string, 2)
	record := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, e vlatte.OnComplete) error {
		got <- e.Parameters().(string)
		return nil
	})

	m := MustNew(Setup().
		Silent().
		OnCompleteListener("record", record).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "record", Parameters: "global"}}}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").
		OnCompleteEvent(vlatte.EventConfig{Name: "record", Parameters: "mock"}).
		Reply(Message(&pb.HiRes{})))

	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"global", "mock"}, []string{<-got, <-got})

	_, err = m.Mock(Method("Greetings").OnCompleteEvent(vlatte.EventConfig{Name: "nobody"}).Reply(Message(&pb.HiRes{})))
	require.ErrorContains(t, err, "mock: on_complete listener nobody at index 0 is not registered")
}

func TestBuilder_FieldReadsTheRequestAsProtoJSON(t *testing.T) {
	// The request was read as encoding/json writes the Go struct: enums as numbers, a oneof under its Go wrapper, a
	// timestamp as its seconds and nanos. It reads as protojson writes it now, with the .proto field names, as the
	// JSON of every other tool does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("TestService/Inspect").
		Field("user_id", match.Equal("u1")).
		Field("count", match.Equal("42")).
		Field("color", match.Equal("COLOR_RED")).
		Field("name", match.Equal("ann")).
		Field("labels.team", match.Equal("core")).
		Field("nested.message", match.Equal("deep")).
		Field("at", match.Equal("2026-01-02T03:04:05Z")).
		Field("tags[1]", match.Equal("b")).
		Reply(Message(&pb.Rich{UserId: "matched"})))

	res, err := pb.NewTestServiceClient(m.MustDial()).Inspect(t.Context(), &pb.Rich{
		UserId: "u1",
		Count:  42,
		Color:  pb.Color_COLOR_RED,
		Choice: &pb.Rich_Name{Name: "ann"},
		Labels: map[string]string{"team": "core"},
		Nested: &pb.HiReq{Message: "deep"},
		At:     timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		Tags:   []string{"a", "b"},
	})
	require.NoError(t, err)
	require.Equal(t, "matched", res.GetUserId())
}

func TestBuilder_JSONMessageAndCallMatchesMatchTheWholeRequest(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Greetings").JSON(`{"message": "json"}`).Reply(Message(&pb.HiRes{Message: "by json"})),
		Method("Greetings").Message(&pb.HiReq{Message: "proto"}).Reply(Message(&pb.HiRes{Message: "by message"})),
		Method("Greetings").CallMatches(func(call *Call) (bool, error) {
			return call.Message().(*pb.HiReq).GetMessage() == "func", nil
		}).Reply(Message(&pb.HiRes{Message: "by func"})),
	)

	for give, want := range map[string]string{"json": "by json", "proto": "by message", "func": "by func"} {
		got, code := greetWith(t, c, give)
		require.Equal(t, codes.OK, code, give)
		require.Equal(t, want, got)
	}

	_, code := greetWith(t, c, "other")
	require.Equal(t, codes.NotFound, code)

	_, err := m.Mock(Method("Greetings").JSON(`{"message":`).Reply(Message(&pb.HiRes{})))
	require.ErrorContains(t, err, "json: not JSON")
}

func TestBuilder_MatchesGivesTheMatcherTheCall(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(Method("Greetings").Matches(match.Function(func(v any) (bool, error) {
		call, ok := v.(*Call)
		return ok && call.Message().(*pb.HiReq).GetMessage() == "ann", nil
	})).Reply(Message(&pb.HiRes{Message: "hi ann"})))

	got, code := greetWith(t, c, "ann")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi ann", got)

	_, code = greetWith(t, c, "bob")
	require.Equal(t, codes.NotFound, code)

	_, err := m.Mock(Method("Chat").Matches(match.Not(match.Nil())).Reply(Bidi()))
	require.ErrorContains(t, err, "a bidi streaming mock matches the method and the header only", "it reads the request")
}

func TestBuilder_ABidiMockMatchesNoRequest(t *testing.T) {
	// A bidi call matches as it starts, before its client sent anything: a matcher of its request would never match.
	m := serve(t)

	_, err := m.Mock(Method("Chat").Field("message", match.Equal("hi")).Reply(Bidi()))
	require.ErrorContains(t, err, "a bidi streaming mock matches the method and the header only")

	_, err = m.Mock(Method("Chat").Header("x-team", match.Equal("core")).Reply(Bidi()))
	require.NoError(t, err)
}

func TestBuilder_JSONThatIsNoRequestOfTheMethodFailsTheMock(t *testing.T) {
	// A JSON request that can't be a request of the method would never match: the mock fails when it's added.
	m := serve(t)

	for _, tc := range []struct {
		name string
		b    *Builder
		want string
	}{
		{"unknown field", Method("Greetings").JSON(`{"nope": 1}`), "mock: Greetings: json:"},
		{"stream without an array", Method("Upload").JSON(`{"key": "a"}`), "is an array of messages"},
		{"stream with a bad message", Method("Upload").JSON(`[{"key": "a"}, {"nope": 1}]`), "json: message 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(tc.b.Reply(Message(&pb.HiRes{})))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuilder_HeaderMatchesEveryValueOfAKeySentTwice(t *testing.T) {
	// A key the client sent twice is matched as the list of its values, not by its first one.
	m := serve(t)
	m.MustMock(Method("Greetings").Header("x-team", match.Equal([]string{"a", "b"})).Reply(Message(&pb.HiRes{Message: "both"})))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "a", "x-team", "b")
	res, err := client(t, m).Greetings(ctx, &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "both", res.GetMessage())
}

func TestBuilder_AFailingCallbackDoesntChangeTheAnswer(t *testing.T) {
	done := make(chan struct{})
	m := serve(t)
	m.MustMock(Method("Greetings").
		Callback(func(context.Context, vlatte.OnComplete) error {
			defer close(done)
			return errors.New("broken")
		}).
		Reply(Message(&pb.HiRes{Message: "answered"})))

	res, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "answered", res.GetMessage())
	<-done
}

// scenarioServer runs the scenario suite on a server whose mocks are built in Go, or from mock files.
type scenarioServer struct {
	*Server

	c         pb.TestServiceClient
	fromFiles bool
}

var _ scenariotest.Server = (*scenarioServer)(nil)

func newScenarioServer(fromFiles bool) func(t *testing.T) scenariotest.Server {
	return func(t *testing.T) scenariotest.Server {
		m := serve(t)

		return &scenarioServer{Server: m, c: client(t, m), fromFiles: fromFiles}
	}
}

func (s *scenarioServer) Mock(t *testing.T, mocks ...scenariotest.Mock) {
	for _, mk := range mocks {
		if s.fromFiles {
			doc, err := json.Marshal(map[string]any{
				"id": mk.ID,
				"scenario": map[string]string{
					"name": mk.Scenario, "required_state": mk.RequiredState, "new_state": mk.NewState,
				},
				"request":  map[string]string{"method": "Greetings"},
				"response": map[string]any{"message": map[string]string{"message": mk.ID}},
			})
			require.NoError(t, err)

			s.MustMock(FromBytes(doc, "json"))

			continue
		}

		b := Method("Greetings").ID(mk.ID).ScenarioIs(mk.Scenario).Reply(Message(&pb.HiRes{Message: mk.ID}))
		if mk.RequiredState != "" {
			b.ScenarioStateIs(mk.RequiredState)
		}

		if mk.NewState != "" {
			b.ScenarioStateWillBe(mk.NewState)
		}

		s.MustMock(b)
	}
}

func (s *scenarioServer) Call(t *testing.T) string {
	got, code := greetWith(t, s.c, "")

	switch code {
	case codes.OK:
		return got
	case codes.NotFound:
		return ""
	default:
		t.Fatalf("want a mock's answer or no match, got %s", code)
		return ""
	}
}

func TestBuilder_ServesScenarios(t *testing.T) {
	scenariotest.Run(t, newScenarioServer(false))
}
