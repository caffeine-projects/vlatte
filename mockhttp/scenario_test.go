package mockhttp

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// scenarioEvents follows the admin API's events of m while send runs, and returns the first n scenario events. It
// waits 5 seconds at most, so a missing event fails the test rather than hangs it.
func scenarioEvents(t *testing.T, m *Server, n int, send func()) []map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, m.AdminURL()+"/events", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	send()

	lines := bufio.NewScanner(res.Body)

	var events []map[string]any
	for len(events) < n && lines.Scan() {
		line, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}

		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &e))

		if e["type"] == "scenario" {
			delete(e, "time")
			events = append(events, e)
		}
	}

	require.NoError(t, lines.Err())
	require.Len(t, events, n, "events: %v", events)

	return events
}

func TestScenarios_TemplatesReadTheStateOfAScenario(t *testing.T) {
	// One mock answers in every state of a scenario, with what the state says, instead of a mock per state. It reads
	// the state its own mock moved the scenario to.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/order").Reply(OK().BodyTemplate(`{{ .Scenarios.State "Checkout" }}`)),
		Postf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").
			Reply(OK().BodyTemplate(`{{ .Scenarios.State "checkout" }}`)),
	)

	for _, step := range []struct {
		get  bool
		path string
		want string
	}{
		{true, "/order", matchscenario.StateStarted},
		{false, "/pay", "paid"},
		{true, "/order", "paid"},
	} {
		var body string
		if step.get {
			_, body = get(t, m, step.path)
		} else {
			_, b := do(t, http.MethodPost, m.URL(step.path), nil, nil)
			body = string(b)
		}

		require.Equal(t, step.want, body, step.path)
	}
}

func TestScenarios_TheJournalKeepsTheMovesOfARequest(t *testing.T) {
	// A test verifies which request moved a scenario: its entry has the move, and a request whose mock moves none has
	// none.
	m := journaledServer(t, Setup())
	m.MustMock(
		Postf("/pay").ScenarioIs("checkout").ScenarioStateIs(matchscenario.StateStarted).ScenarioStateWillBe("paid").
			Reply(OK()),
		Getf("/order").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(OK()),
	)

	do(t, http.MethodPost, m.URL("/pay"), nil, nil)
	get(t, m, "/order")

	pay := only(t, m, journal.Query{Path: match.Equal("/pay")})
	require.Equal(t, []matchscenario.Move{{Scenario: "checkout", From: matchscenario.StateStarted, To: "paid"}},
		pay.Scenarios)

	order := only(t, m, journal.Query{Path: match.Equal("/order")})
	require.Equal(t, journal.OutcomeMatched, order.Outcome)
	require.Empty(t, order.Scenarios)
}

func TestScenarios_StreamsEachMove(t *testing.T) {
	// A client, such as the admin page, follows the scenarios as they move: by a request, which names its mock, or by
	// the server's methods and the admin API.
	m := adminServer(t, Setup())
	m.MustMock(Postf("/pay").ID("pay").StartScenario("checkout").ScenarioStateWillBe("paid").Reply(OK()))

	var paid *http.Response

	events := scenarioEvents(t, m, 4, func() {
		paid, _ = do(t, http.MethodPost, m.URL("/pay"), nil, nil)
		require.NoError(t, m.SetScenario(t.Context(), "checkout", matchscenario.StateStarted))
		m.ResetScenario("checkout")
		m.ResetScenarios()
	})

	require.Equal(t, paid.Header.Get(vlatte.HeaderRequestID), events[0]["request_id"], "the event names its request")
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
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)
	m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}

	_, err := m.Scenarios(t.Context())
	require.EqualError(t, err, "http: listing mocks: store down")

	err = m.SetScenario(t.Context(), "checkout", "paid")
	require.EqualError(t, err, "http: listing mocks: store down")
}
