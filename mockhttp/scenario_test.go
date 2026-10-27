package mockhttp

import (
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
