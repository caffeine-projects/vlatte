package matchscenario

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestStore_SetMovesAScenarioMocksMatchNext(t *testing.T) {
	store := New()
	require.True(t, match(t, store.Matcher("checkout", StateStarted, "paid")))

	store.SetScenario("checkout", "paid")
	store.SetScenario("Refund", "asked")
	require.Equal(t, map[string]string{"checkout": "paid", "Refund": "asked"}, store.Scenarios())

	require.True(t, match(t, store.Matcher("checkout", "paid", "")), "a mock that requires the state set matches")
	require.False(t, match(t, store.Matcher("refund", "done", "")), "a scenario set before any mock started it has the state set")
}

func TestStore_SetKeepsTheNameAScenarioWasFirstGiven(t *testing.T) {
	// The admin API names a scenario in its path: any case moves it, and the list keeps the name of the mocks.
	store := New()
	store.SetScenario("Checkout", "paid")
	store.SetScenario("checkout", "refunded")

	require.Equal(t, map[string]string{"Checkout": "refunded"}, store.Scenarios())
}

func TestStore_ResetStartsEveryScenarioOver(t *testing.T) {
	store := New()
	pay := store.Matcher("checkout", StateStarted, "paid")
	require.True(t, match(t, pay))
	require.True(t, pay.Claim())
	store.SetScenario("Refund", "asked")

	store.ResetScenarios()

	require.Equal(t, map[string]string{"checkout": StateStarted, "Refund": StateStarted}, store.Scenarios())
	require.True(t, match(t, pay), "the scenario starts over")
}

func TestStore_ScenariosIsACopy(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid")

	states := store.Scenarios()
	states["checkout"] = "refunded"

	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestStore_IsSafeForConcurrentUse(t *testing.T) {
	// Requests match and move scenarios on many goroutines while the admin API sets and resets them: the race
	// detector fails this test when the store isn't safe.
	store := New()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			m := store.Matcher("checkout", StateStarted, "paid")
			m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
			m.Claim()
			store.SetScenario("Checkout", "refunded")
			store.Scenarios()
			store.ResetScenarios()
		})
	}
	wg.Wait()

	require.Len(t, store.Scenarios(), 1)
}
