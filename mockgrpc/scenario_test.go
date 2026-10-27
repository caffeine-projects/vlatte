package mockgrpc

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestScenarios_TemplatesAndTheJournalSeeTheMove(t *testing.T) {
	// A call's template reads the state its own mock moved the scenario to, and its journal entry keeps the move, as
	// an HTTP request's does.
	m, c := journaled(t, Setup())
	m.MustMock(Method("Greetings").StartScenario("checkout").ScenarioStateWillBe("paid").
		Reply(Template(`{"message": "{{ .Scenarios.State "Checkout" }}"}`)))

	got, code := greetWith(t, c, "")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "paid", got)

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, []matchscenario.Move{{Scenario: "checkout", From: matchscenario.StateStarted, To: "paid"}},
		e.Scenarios)
}

func TestScenarios_StreamsEachMove(t *testing.T) {
	// A client, such as the admin page, follows the scenarios as they move: by a call, which names its mock, or by
	// the server's methods and the admin API.
	m, c := journaled(t, Setup().Admin(admin.MustNew()))
	m.MustMock(Method("Greetings").ID("pay").StartScenario("checkout").ScenarioStateWillBe("paid").
		Reply(Message(&pb.HiRes{})))

	// A missing event fails the test, rather than hangs it.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, m.AdminURL()+"/events", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	_, code := greetWith(t, c, "")
	require.Equal(t, codes.OK, code)
	require.NoError(t, m.SetScenario(t.Context(), "checkout", matchscenario.StateStarted))
	m.ResetScenario("checkout")
	m.ResetScenarios()

	lines := bufio.NewScanner(res.Body)

	var events []map[string]any
	for len(events) < 4 && lines.Scan() {
		if line, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
			var e map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &e))

			if e["type"] == "scenario" {
				delete(e, "time")
				events = append(events, e)
			}
		}
	}

	require.NoError(t, lines.Err())
	require.Len(t, events, 4, "events: %v", events)
	require.Equal(t, onlyCall(t, m, journal.Query{}).ID, events[0]["request_id"], "the event names its call")
	delete(events[0], "request_id")

	require.Equal(t, []map[string]any{
		{"type": "scenario", "target": "checkout", "mock_id": "pay", "message": "checkout moved from started to paid"},
		{"type": "scenario", "target": "checkout", "message": "checkout set to started"},
		{"type": "scenario", "target": "checkout", "message": "checkout reset to started"},
		{"type": "scenario", "message": "every scenario reset to started"},
	}, events)
}

func TestScenarios_AStoreThatFailsFailsTheCall(t *testing.T) {
	// A server whose mocks can't be listed can't say which states its scenarios have: it fails, rather than list none
	// or move a scenario to a state no mock names.
	m := MustNew()
	t.Cleanup(m.Close)
	m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}

	_, err := m.Scenarios(t.Context())
	require.EqualError(t, err, "grpc: listing mocks: store down")

	err = m.SetScenario(t.Context(), "checkout", "paid")
	require.EqualError(t, err, "grpc: listing mocks: store down")
}

func TestScenarios_EachKeyPlaysInAnInstanceOfItsOwn(t *testing.T) {
	// A call's key renders with the call, and its template and journal entry see the instance of that key.
	m, c := journaled(t, Setup())
	m.MustMock(Method("Greetings").StartScenario("checkout").ScenarioStateWillBe("paid").ScenarioKey(_scenarioKey).
		Reply(Template(`{"message": "{{ .Scenarios.StateOf "checkout" .Request.Message.message }}"}`)))

	got, code := greetWith(t, c, "t1")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "paid", got)

	_, code = greetWith(t, c, "t1")
	require.Equal(t, codes.NotFound, code, "t1 paid once")

	got, code = greetWith(t, c, "t2")
	require.Equal(t, codes.OK, code, "t2 pays in its own instance")
	require.Equal(t, "paid", got)

	e := onlyCall(t, m, journal.Query{Body: match.Contain("t2")})
	require.Equal(t, []matchscenario.Move{{Scenario: "checkout", Key: "t2", From: matchscenario.StateStarted, To: "paid"}},
		e.Scenarios)
}

func TestScenarios_AKeyThatFailsToRenderFailsTheCall(t *testing.T) {
	// A key that can't render is a mistake of the mock: the call fails, rather than play in the shared instance.
	m := serve(t)
	m.MustMock(Method("Greetings").StartScenario("checkout").ScenarioKey(`{{ .Request.Nope }}`).Reply(Message(&pb.HiRes{})))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.Internal, status.Code(err))
	require.ErrorContains(t, err, "scenario: key: template: scenario.key")

	_, err = m.Mock(Method("Greetings").StartScenario("checkout").ScenarioKey(`{{`).Reply(Message(&pb.HiRes{})))
	require.ErrorContains(t, err, "mock: scenario.key: template: scenario.key")
}
