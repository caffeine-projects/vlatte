package mockhttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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
