package vlatte_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// request is a request of no protocol: its parts by name.
type request map[string]string

// part returns an evaluator of the part name of a request.
func part(name string, m vlatte.Matcher) *vlatte.Eval[request] {
	return &vlatte.Eval[request]{
		Key:      name,
		Matcher:  m,
		Selector: func(_ context.Context, r request) any { return r[name] },
	}
}

// statefulMatcher is a matcher that keeps state, such as a scenario's, and counts the times it runs.
type statefulMatcher struct {
	runs int
}

func (m *statefulMatcher) Name() string                          { return "stateful" }
func (m *statefulMatcher) Match(vlatte.MatcherArg) (bool, error) { m.runs++; return false, nil }
func (m *statefulMatcher) SetState() error                       { return nil }

func nearMisses(t *testing.T, mocks map[*fakeMock][]*vlatte.Eval[request], order []*fakeMock, in request) []vlatte.NearMiss {
	seq := vlatte.Mocks[*fakeMock, request](slices.Values(order))

	return seq.NearMisses(t.Context(), in,
		func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] },
		func(e *vlatte.Eval[request]) string { return e.Key })
}

func TestNearMisses_RanksTheClosestMocks(t *testing.T) {
	// A request no mock matched is a test's puzzle: the report names the mocks that came closest, those that differ
	// in the fewest parts first, then those that matched the most, and says which parts differed. A mock that
	// matched nothing is no hint, and three are enough to read.
	in := request{"method": "GET", "path": "/a", "h": "1"}

	m1, m2, m3, m4, m5 := newFakeMockWith("m1", 0), newFakeMockWith("m2", 0), newFakeMockWith("m3", 0),
		newFakeMockWith("m4", 0), newFakeMockWith("m5", 0)

	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		m1: {part("method", match.Equal("GET")), part("path", match.Equal("/b"))},
		m2: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("2"))},
		m3: {part("method", match.Equal("POST"))},
		m4: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("2")), part("x", match.Equal("y"))},
		m5: {part("method", match.Equal("GET"))},
	}

	got := nearMisses(t, mocks, []*fakeMock{m1, m2, m3, m4, m5}, in)

	require.Equal(t, []vlatte.NearMiss{
		{MockID: "m5", Name: "m5", Passed: 1, Total: 1},
		{MockID: "m2", Name: "m2", Passed: 2, Total: 3, Failed: []string{"h"}},
		{MockID: "m1", Name: "m1", Passed: 1, Total: 2, Failed: []string{"path"}},
	}, got)
}

func TestNearMisses_SkipsMatchersThatKeepState(t *testing.T) {
	// A scenario's matcher may start the scenario as it runs: reporting a near miss must change nothing, so such
	// matchers never run, and the report names them, as the reason a mock whose other parts all matched didn't.
	in := request{"method": "GET", "path": "/a"}
	scenario := &statefulMatcher{}
	m, only, far := newFakeMockWith("m", 0), newFakeMockWith("only", 0), newFakeMockWith("far", 0)

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		far:  {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("1"))},
		m:    {part("method", match.Equal("GET")), {Key: "scenario", Matcher: scenario}},
		only: {{Key: "times", Matcher: &statefulMatcher{}}},
	}, []*fakeMock{far, m, only}, in)

	require.Zero(t, scenario.runs)
	require.Equal(t, []vlatte.NearMiss{
		{MockID: "m", Name: "m", Passed: 1, Total: 1, Skipped: []string{"scenario"}},
		{MockID: "only", Name: "only", Skipped: []string{"times"}},
		{MockID: "far", Name: "far", Passed: 2, Total: 3, Failed: []string{"h"}},
	}, got)
}

func TestNearMisses_AFailingMatcherFailsItsPart(t *testing.T) {
	// A matcher that fails, or panics, is a part that didn't match, with its error: the report goes on with the
	// next ones.
	in := request{"method": "GET"}
	m := newFakeMockWith("m", 0)

	failing := vlatte.MatcherFunc("failing", func(vlatte.MatcherArg) (bool, error) { return false, errors.New("bad") })
	panicking := vlatte.MatcherFunc("panicking", func(vlatte.MatcherArg) (bool, error) { panic("boom") })

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		m: {part("a", failing), part("b", panicking), part("method", match.Equal("GET"))},
	}, []*fakeMock{m}, in)

	require.Len(t, got, 1)
	require.Equal(t, 1, got[0].Passed)
	require.Equal(t, 3, got[0].Total)
	require.Len(t, got[0].Failed, 2)
	require.Equal(t, "a: bad", got[0].Failed[0], "an error names itself: a broken matcher isn't a request that differs")
	require.Contains(t, got[0].Failed[1], "b: ")
	require.Contains(t, got[0].Failed[1], "boom")
}
