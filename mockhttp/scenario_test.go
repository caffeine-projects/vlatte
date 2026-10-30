package mockhttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

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

func TestScenarios_AKeyThatFailsToRenderFailsTheRequest(t *testing.T) {
	// A key that can't render is a mistake of the mock: the request fails to match with its error, rather than play
	// in the shared instance.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/pay").StartScenario("checkout").ScenarioKey(`{{ .Request.Nope }}`).Reply(OK()))

	res, body := do(t, http.MethodPost, m.URL("/pay"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureMatch, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "scenario: key: template: scenario.key")

	_, err := m.Mock(Postf("/pay").StartScenario("checkout").ScenarioKey(`{{`).Reply(OK()))
	require.ErrorContains(t, err, "mock: scenario.key: template: scenario.key")
}

func TestScenarios_AKeyReadsTheKVStore(t *testing.T) {
	// A key can read the key-value store, such as the tenant a test sets: the request plays in the instance of its
	// value.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "tenant", []byte("acme")))

	m := MustNew(Setup().Silent().KV(kv))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").ScenarioKey(`{{ .KV.Get "tenant" }}`).
		Reply(OK()))

	res, body := do(t, http.MethodPost, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	got, err := m.Scenarios(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, matchscenario.StateStarted, got[0].State, "the shared instance")
	require.Equal(t, []matchscenario.KeyedInstance{{Key: "acme", State: "paid"}}, got[0].Instances)
}
