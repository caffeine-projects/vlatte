package matchscenario

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// requestArg is the argument a scenario's matcher gets for a request, in Match and Claim.
func requestArg(t *testing.T) vlatte.MatcherArg {
	return vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil)
}

func match(t *testing.T, m *Matcher) bool {
	t.Helper()

	ok, err := m.Match(requestArg(t))
	require.NoError(t, err)

	return ok
}

func TestMatcher_AScenarioMovesThroughItsStates(t *testing.T) {
	store := New()
	pay := store.Matcher("checkout", StateStarted, "paid")
	refund := store.Matcher("checkout", "paid", "refunded")

	require.True(t, match(t, pay))
	require.False(t, match(t, refund), "the scenario is still where it started")
	require.True(t, pay.Claim(requestArg(t)))

	require.False(t, match(t, pay), "the scenario moved on to paid")
	require.True(t, match(t, refund))
	require.True(t, refund.Claim(requestArg(t)))
	require.Equal(t, map[string]string{"checkout": "refunded"}, store.Scenarios())
}

func TestMatcher_MatchingChangesNoState(t *testing.T) {
	// A report of a request no mock matched runs the matcher again: matching must move nothing, only the mock picked
	// does.
	store := New()

	require.True(t, match(t, store.Matcher("checkout", StateStarted, "paid")))
	require.Empty(t, store.Scenarios())
}

func TestMatcher_AScenarioNoMockMovedIsInStarted(t *testing.T) {
	// A mock that requires paid waits for the scenario to get there, even before anything moved it: a typo in a
	// scenario's name, or a missing first mock, makes the mock never match, rather than match every request.
	store := New()

	require.False(t, match(t, store.Matcher("checkout", "paid", "")))
	require.False(t, store.Matcher("checkout", "paid", "refunded").Claim(requestArg(t)))
	require.True(t, match(t, store.Matcher("checkout", StateStarted, "")))
	require.Empty(t, store.Scenarios(), "nothing moved")
}

func TestMatcher_NoRequiredStateMatchesInAnyState(t *testing.T) {
	// A mock that requires no state matches in every state of its scenario, so one mock can answer throughout a flow.
	store := New()
	anyState := store.Matcher("checkout", "", "")

	require.True(t, match(t, anyState), "before the scenario starts")

	for _, state := range []string{StateStarted, "paid"} {
		store.SetScenario("checkout", state)
		require.True(t, match(t, anyState), state)
	}
}

func TestMatcher_AClaimLostToAnotherRequestMovesNothing(t *testing.T) {
	// Two requests in started both match the mock of started; the one that claims second must not move the scenario
	// again, so it looks for the mock of paid instead.
	store := New()
	first := store.Matcher("checkout", StateStarted, "paid")
	second := store.Matcher("checkout", StateStarted, "paid")

	require.True(t, match(t, first))
	require.True(t, match(t, second))

	require.True(t, first.Claim(requestArg(t)))
	require.False(t, second.Claim(requestArg(t)))
	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestMatcher_ANewStateStartsTheScenario(t *testing.T) {
	// A mock that requires no state still moves its scenario, one nothing moved yet too.
	store := New()
	move := store.Matcher("checkout", "", "paid")

	require.True(t, match(t, move))
	require.True(t, move.Claim(requestArg(t)))
	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestMatcher_NoNewStateLeavesTheScenarioWhereItIs(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid")

	require.True(t, store.Matcher("checkout", "paid", "").Claim(requestArg(t)))
	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestMatcher_NamesIgnoreCase(t *testing.T) {
	// Mocks that write a scenario's name in different cases share it: a name in the wrong case would match every
	// request and never move, a mistake no one sees in a mock file.
	store := New()
	pay := store.Matcher("Checkout", StateStarted, "paid")

	require.True(t, match(t, pay))
	require.True(t, pay.Claim(requestArg(t)))

	require.False(t, match(t, store.Matcher("checkout", StateStarted, "")))
	require.True(t, match(t, store.Matcher("CHECKOUT", "paid", "")))
	require.Equal(t, map[string]string{"Checkout": "paid"}, store.Scenarios())
}

func TestMatcher_NamesIgnoreCaseAsEqualFoldDoes(t *testing.T) {
	// Lowercasing alone keeps σ and ς, or K and the Kelvin sign, two scenarios, though strings.EqualFold finds them
	// equal.
	for _, names := range [][2]string{{"σ", "ς"}, {"ΣΑΣ", "σας"}, {"K", "\u212a"}} {
		t.Run(names[0], func(t *testing.T) {
			store := New()
			require.True(t, store.Matcher(names[0], "", "paid").Claim(requestArg(t)))

			refund := store.Matcher(names[1], "paid", "refunded")
			require.True(t, match(t, refund))
			require.True(t, refund.Claim(requestArg(t)))
			require.Equal(t, map[string]string{names[0]: "refunded"}, store.Scenarios())
		})
	}
}

func TestMatcher_StoresAreIsolated(t *testing.T) {
	// Each mock server has its own store: a scenario of one never moves the scenario of the same name of another.
	a, b := New(), New()
	payA := a.Matcher("checkout", StateStarted, "paid")

	require.True(t, match(t, payA))
	require.True(t, payA.Claim(requestArg(t)))

	require.True(t, match(t, b.Matcher("checkout", StateStarted, "")))
	require.Empty(t, b.Scenarios())
}

func TestMatcher_StringSaysWhyItFails(t *testing.T) {
	// A report of a request no mock matched names the scenario, the state the mock needs, and the one it is in.
	store := New()
	store.SetScenario("checkout", "paid")

	require.Equal(t, "scenario checkout needs started, is paid", store.Matcher("checkout", StateStarted, "").String())
	require.Equal(t, "scenario checkout", store.Matcher("checkout", "", "refunded").String())
}

func TestMatcher_DescribesAsTheScenarioOfAMockFile(t *testing.T) {
	m := New().Matcher("checkout", StateStarted, "paid")

	require.Equal(t, "Scenario", m.Name())
	require.Equal(t, descriptor.SingleArg("scenario", map[string]any{
		"name":           "checkout",
		"required_state": StateStarted,
		"new_state":      "paid",
	}), m.Describe())
}

func TestMatcher_MoveIsWhatAPickDoes(t *testing.T) {
	// A server records the move of the mock it picked, in its journal and its events: from the state the mock
	// requires, to its new state.
	store := New()

	mv, ok := store.Matcher("checkout", StateStarted, "paid").Move()
	require.True(t, ok)
	require.Equal(t, Move{Scenario: "checkout", From: StateStarted, To: "paid"}, mv)
	require.Equal(t, "checkout moved from started to paid", mv.String())

	mv, ok = store.Matcher("checkout", "", "paid").Move()
	require.True(t, ok)
	require.Equal(t, "checkout moved to paid", mv.String())

	_, ok = store.Matcher("checkout", "paid", "").Move()
	require.False(t, ok, "a mock without a new state moves nothing")
}
