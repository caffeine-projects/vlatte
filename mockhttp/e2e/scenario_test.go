package e2e

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestScenarios(t *testing.T) {
	testCases := []struct {
		name string
		s1   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
		s2   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
		s3   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
	}{
		{
			"code",
			mockhttp.Get(match.URLPath("/1")).
				StartScenario("code").
				ScenarioStateWillBe("step2").
				Name("step-1").
				Reply(mockhttp.OK().PlainText("step1")),
			mockhttp.Get(match.URLPath("/2")).
				ScenarioIs("code").
				ScenarioStateIs("step2").
				ScenarioStateWillBe("step3").
				Name("step-2").
				Reply(mockhttp.OK().PlainText("step2")),
			mockhttp.Get(match.URLPath("/3")).
				ScenarioIs("code").
				ScenarioStateIs("step3").
				ScenarioStateWillBe("step4").
				Name("step-3").
				Reply(mockhttp.OK().PlainText("step3")),
		},

		{
			"file",
			mockhttp.FromFile("testdata/scenario/step_1.yaml"),
			mockhttp.FromFile("testdata/scenario/step_2.yaml"),
			mockhttp.FromFile("testdata/scenario/step_3.yaml"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := mockhttp.MustNew()
			t.Cleanup(func() { m.Close() })
			m.MustStart()

			s1 := m.MustMock(tc.s1)
			s2 := m.MustMock(tc.s2)
			s3 := m.MustMock(tc.s3)

			// --- step1

			req, _ := http.NewRequest(http.MethodGet, m.URL()+"/1", nil)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s1.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step1", string(body))
			require.NoError(t, res.Body.Close())

			// step1 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/1", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

			// --- step2

			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/2", nil)
			res, err = http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err = io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s2.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step2", string(body))
			require.NoError(t, res.Body.Close())

			// step2 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/2", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

			// --- step3

			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/3", nil)
			res, err = http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err = io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s3.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step3", string(body))
			require.NoError(t, res.Body.Close())

			// step3 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/3", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
		})
	}
}

// scenarioEvents follows the admin API's events of m while send runs, and returns the first n scenario events. It
// waits 5 seconds at most, so a missing event fails the test rather than hangs it.
func scenarioEvents(t *testing.T, m *mockhttp.Server, n int, send func()) []map[string]any {
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
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.Getf("/order").Reply(mockhttp.OK().BodyTemplate(`{{ .Scenarios.State "Checkout" }}`)),
		mockhttp.Postf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").
			Reply(mockhttp.OK().BodyTemplate(`{{ .Scenarios.State "checkout" }}`)),
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
		var body []byte
		if step.get {
			_, body = getTarget(t, m, step.path)
		} else {
			_, body = postBody(t, m.URL(step.path), "", nil)
		}

		require.Equal(t, step.want, string(body), step.path)
	}
}

func TestScenarios_TheJournalKeepsTheMovesOfARequest(t *testing.T) {
	// A test verifies which request moved a scenario: its entry has the move, and a request whose mock moves none has
	// none.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(t.TempDir()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.Postf("/pay").ScenarioIs("checkout").ScenarioStateIs(matchscenario.StateStarted).
			ScenarioStateWillBe("paid").Reply(mockhttp.OK()),
		mockhttp.Getf("/order").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(mockhttp.OK()),
	)

	postBody(t, m.URL("/pay"), "", nil)
	getTarget(t, m, "/order")

	only := func(path string) *journal.Entry {
		entries, err := m.Requests(t.Context(), journal.Query{Path: match.Equal(path)})
		require.NoError(t, err)
		require.Len(t, entries, 1)

		return entries[0]
	}

	pay := only("/pay")
	require.Equal(t, []matchscenario.Move{{Scenario: "checkout", From: matchscenario.StateStarted, To: "paid"}},
		pay.Scenarios)

	order := only("/order")
	require.Equal(t, journal.OutcomeMatched, order.Outcome)
	require.Empty(t, order.Scenarios)
}

func TestScenarios_StreamsEachMove(t *testing.T) {
	// A client, such as the admin page, follows the scenarios as they move: by a request, which names its mock, or by
	// the server's methods and the admin API.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Postf("/pay").ID("pay").StartScenario("checkout").ScenarioStateWillBe("paid").
		Reply(mockhttp.OK()))

	var paid *http.Response

	events := scenarioEvents(t, m, 4, func() {
		paid, _ = postBody(t, m.URL("/pay"), "", nil)
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

// holdFirst is a template data provider that holds the first request of path once its mock matched: it says so on
// matched, then waits for release.
type holdFirst struct {
	path    string
	once    *sync.Once
	matched chan struct{}
	release chan struct{}
}

func (p holdFirst) Key() string { return "hold" }

func (p holdFirst) Data(in *mockhttp.TemplateDataProviderInput) (any, error) {
	if in.Request().URL.Path == p.path {
		p.once.Do(func() {
			close(p.matched)
			<-p.release
		})
	}

	return nil, nil
}

func TestScenarios_ATemplateReadsTheMoveOfItsOwnRequest(t *testing.T) {
	// A request's answer says the state its own mock moved the scenario to, as its journal entry does, even when
	// another request moved the scenario on before the answer was rendered, such as during the mock's delay.
	hold := holdFirst{path: "/pay", once: new(sync.Once), matched: make(chan struct{}), release: make(chan struct{})}
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateDataProviders([]mockhttp.TemplateDataProvider{hold}))
	m.MustStart()
	t.Cleanup(m.Close)

	// A test that fails before it releases the request still ends: Close waits for the request.
	release := sync.OnceFunc(func() { close(hold.release) })
	t.Cleanup(release)

	m.MustMock(
		mockhttp.Postf("/pay").ScenarioIs("checkout").ScenarioStateIs(matchscenario.StateStarted).
			ScenarioStateWillBe("paid").Reply(mockhttp.OK().BodyTemplate(`{{ .Scenarios.State "checkout" }}`)),
		mockhttp.Postf("/ship").ScenarioIs("checkout").ScenarioStateIs("paid").ScenarioStateWillBe("shipped").
			Reply(mockhttp.OK()),
	)

	paid := make(chan string, 1)

	go func() {
		res, err := http.Post(m.URL("/pay"), "", nil)
		if !assert.NoError(t, err) {
			paid <- ""
			return
		}

		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		paid <- string(b)
	}()

	<-hold.matched

	res, _ := postBody(t, m.URL("/ship"), "", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the next request matches in the state /pay moved to")
	release()

	require.Equal(t, "paid", <-paid)
}

func TestScenarios_ResettingAScenarioNothingNamesStreamsNoMove(t *testing.T) {
	// A client following the events sees only scenarios that exist: one no mock names and nobody set doesn't move.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()))
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
	// A client that follows the events ends on the state the scenario is in: requests that move it at once stream
	// their moves in the order they made them.
	const steps = 64

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	state := func(i int) string {
		if i == 0 {
			return matchscenario.StateStarted
		}

		return fmt.Sprintf("s%d", i)
	}

	want := make([]string, 0, steps)
	for i := range steps {
		m.MustMock(mockhttp.Postf("/next").ScenarioIs("checkout").ScenarioStateIs(state(i)).
			ScenarioStateWillBe(state(i + 1)).Reply(mockhttp.OK()))
		want = append(want, "checkout moved from "+state(i)+" to "+state(i+1))
	}

	events := scenarioEvents(t, m, steps, func() {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range steps / 8 {
					res, err := http.Post(m.URL("/next"), "", nil)
					if assert.NoError(t, err) {
						assert.Equal(t, http.StatusOK, res.StatusCode)
						_ = res.Body.Close()
					}
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
