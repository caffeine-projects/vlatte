package mockgrpc

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	events := scenarioEvents(t, m, 4, func() {
		_, code := greetWith(t, c, "")
		require.Equal(t, codes.OK, code)
		require.NoError(t, m.SetScenario(t.Context(), "checkout", matchscenario.StateStarted))
		m.ResetScenario("checkout")
		m.ResetScenarios()
	})

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

func TestScenarios_AKeyReadsTheKVStore(t *testing.T) {
	// A key can read the key-value store, such as the tenant a test sets: the call plays in the instance of its value.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "tenant", []byte("acme")))

	m, c := journaled(t, Setup().KV(kv))
	m.MustMock(Method("Greetings").StartScenario("checkout").ScenarioStateWillBe("paid").ScenarioKey(`{{ .KV.Get "tenant" }}`).
		Reply(Template(`{"message": "{{ .Scenarios.StateOf "checkout" "acme" }}"}`)))

	got, code := greetWith(t, c, "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "paid", got)

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, []matchscenario.Move{{Scenario: "checkout", Key: "acme", From: matchscenario.StateStarted, To: "paid"}},
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

func TestScenarios_ATemplateReadsTheMoveOfItsOwnCall(t *testing.T) {
	// A call's answer says the state its own mock moved the scenario to, as its journal entry does, even when another
	// call moved the scenario on before the answer was rendered, such as during the mock's delay.
	// The provider holds the first call once its mock matched, until the second was answered.
	var held atomic.Bool
	matched, release := make(chan struct{}), make(chan struct{})
	hold := provider{key: "hold", data: func(*TemplateDataProviderInput) (any, error) {
		if held.CompareAndSwap(false, true) {
			close(matched)
			<-release
		}

		return nil, nil
	}}

	m := MustNew(Setup().Silent().TemplateDataProviders(hold))
	m.MustStart()
	t.Cleanup(m.Close)

	// A test that fails before it releases the call still ends: Close waits for the call.
	unhold := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unhold)

	c := pb.NewTestServiceClient(m.MustDial())
	m.MustMock(
		Method("Greetings").ScenarioIs("checkout").ScenarioStateIs(matchscenario.StateStarted).
			ScenarioStateWillBe("paid").Reply(Template(`{"message": "{{ .Scenarios.State "checkout" }}"}`)),
		Method("Greetings").ScenarioIs("checkout").ScenarioStateIs("paid").ScenarioStateWillBe("shipped").
			Reply(Message(&pb.HiRes{Message: "shipping"})),
	)

	paid := make(chan string, 1)

	go func() {
		res, err := c.Greetings(t.Context(), &pb.HiReq{})
		assert.NoError(t, err)
		paid <- res.GetMessage()
	}()

	<-matched

	got, code := greetWith(t, c, "")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "shipping", got, "the next call matches in the state the first moved to")
	unhold()

	require.Equal(t, "paid", <-paid)
}

func TestScenarios_ResettingAScenarioNothingNamesStreamsNoMove(t *testing.T) {
	// A client following the events sees only scenarios that exist: one no mock names and nobody set doesn't move.
	m := MustNew(Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	events := scenarioEvents(t, m, 1, func() {
		m.ResetScenario("nope")
		require.NoError(t, m.SetScenario(t.Context(), "checkout", "paid"))
	})

	require.Equal(t, []map[string]any{
		{"type": "scenario", "target": "checkout", "message": "checkout set to paid"},
	}, events)
}

func TestScenarios_StreamsConcurrentMovesInTheOrderTheyHappened(t *testing.T) {
	// A client that follows the events ends on the state the scenario is in: calls that move it at once stream their
	// moves in the order they made them.
	const steps = 64

	m := MustNew(Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	c := pb.NewTestServiceClient(m.MustDial())

	state := func(i int) string {
		if i == 0 {
			return matchscenario.StateStarted
		}

		return fmt.Sprintf("s%d", i)
	}

	want := make([]string, 0, steps)
	for i := range steps {
		m.MustMock(Method("Greetings").ScenarioIs("checkout").ScenarioStateIs(state(i)).ScenarioStateWillBe(state(i + 1)).
			Reply(Message(&pb.HiRes{})))
		want = append(want, "checkout moved from "+state(i)+" to "+state(i+1))
	}

	events := scenarioEvents(t, m, steps, func() {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range steps / 8 {
					_, err := c.Greetings(t.Context(), &pb.HiReq{})
					assert.NoError(t, err)
				}
			})
		}
		wg.Wait()
	})

	got := make([]string, 0, steps)
	for _, e := range events {
		got = append(got, e["message"].(string))
	}

	require.Equal(t, want, got)
}
