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

// statefulMatcher is a matcher that keeps state, such as Times or a scenario's, matches as it says, and counts the
// times it runs.
type statefulMatcher struct {
	matches bool
	runs    int
}

func (m *statefulMatcher) Name() string                          { return "stateful" }
func (m *statefulMatcher) Match(vlatte.MatcherArg) (bool, error) { m.runs++; return m.matches, nil }
func (m *statefulMatcher) SetState() error                       { return nil }

// counted is m that counts in runs the times it runs.
func counted(m vlatte.Matcher, runs *int) vlatte.Matcher {
	return vlatte.MatcherFunc(m.Name(), func(arg vlatte.MatcherArg) (bool, error) {
		*runs++
		return m.Match(arg)
	})
}

// nearMisses finds no mock of order for in, as a server does, then reports the closest.
func nearMisses(t *testing.T, mocks map[*fakeMock][]*vlatte.Eval[request], order []*fakeMock, in request) []vlatte.NearMiss {
	t.Helper()

	seq := vlatte.Mocks[*fakeMock, request](slices.Values(order))
	misses := new(vlatte.Misses[*fakeMock, request])

	_, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), in,
		func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] }, misses)
	require.NoError(t, err)
	require.False(t, matched)

	return misses.NearMisses(t.Context(), func(e *vlatte.Eval[request]) string { return e.Key })
}

func TestNearMisses_RanksTheClosestMocks(t *testing.T) {
	// A request no mock matched is a test's puzzle: the report names the mocks that came closest, those that differ
	// in the fewest parts first, then those that matched the most, and says which parts differed. A mock that
	// matched nothing is no hint, and three are enough to read. A mock that differs in its first part only is as
	// close as one that differs in its last.
	in := request{"method": "GET", "path": "/a", "h": "1"}

	m1, m2, m3, m4, m5 := newFakeMockWith("m1", 0), newFakeMockWith("m2", 0), newFakeMockWith("m3", 0),
		newFakeMockWith("m4", 0), newFakeMockWith("m5", 0)

	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		m1: {part("method", match.Equal("GET")), part("path", match.Equal("/b"))},
		m2: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("2"))},
		m3: {part("method", match.Equal("POST"))},
		m4: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("2")), part("x", match.Equal("y"))},
		m5: {part("method", match.Equal("POST")), part("path", match.Equal("/a")), part("h", match.Equal("1"))},
	}

	got := nearMisses(t, mocks, []*fakeMock{m1, m2, m3, m4, m5}, in)

	require.Equal(t, []vlatte.NearMiss{
		{MockID: "m2", Name: "m2", Passed: 2, Total: 3, Failed: []string{"h"}},
		{MockID: "m5", Name: "m5", Passed: 2, Total: 3, Failed: []string{"method"}},
		{MockID: "m1", Name: "m1", Passed: 1, Total: 2, Failed: []string{"path"}},
	}, got)
}

func TestNearMisses_NoMatcherRunsTwice(t *testing.T) {
	// The report goes on from where matching stopped: a matcher costs once per request, and its action, such as
	// Peek's, runs once, however many mocks the request misses.
	in := request{"method": "GET", "path": "/a", "h": "1"}
	a, b := newFakeMockWith("a", 0), newFakeMockWith("b", 0)
	runs := make([]int, 5)

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		a: {part("method", counted(match.Equal("GET"), &runs[0])), part("path", counted(match.Equal("/b"), &runs[1])), part("h", counted(match.Equal("1"), &runs[2]))},
		b: {part("method", counted(match.Equal("POST"), &runs[3])), part("path", counted(match.Equal("/a"), &runs[4]))},
	}, []*fakeMock{a, b}, in)

	require.Equal(t, []int{1, 1, 1, 1, 1}, runs, "each part ran once: in matching, or in the report")
	require.Equal(t, []vlatte.NearMiss{
		{MockID: "a", Name: "a", Passed: 2, Total: 3, Failed: []string{"path"}},
		{MockID: "b", Name: "b", Passed: 1, Total: 2, Failed: []string{"method"}},
	}, got)
}

func TestNearMisses_RunsNoMatcherThatKeepsState(t *testing.T) {
	// A scenario's matcher may start the scenario as it runs: the report must change nothing, so it runs none, and
	// names those matching never reached. One matching ran, and that failed, such as a Times used up, is the reason
	// a mock missed, and no part of the request: a mock that missed by nothing else is kept, and comes first.
	in := request{"method": "GET", "path": "/a"}
	scenario, usedUp, onlyTimes := &statefulMatcher{}, &statefulMatcher{}, &statefulMatcher{}
	m, used, only := newFakeMockWith("m", 0), newFakeMockWith("used", 0), newFakeMockWith("only", 0)

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		m:    {part("path", match.Equal("/b")), part("method", match.Equal("GET")), {Key: "scenario", Matcher: scenario}},
		used: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), {Key: "times", Matcher: usedUp}},
		only: {{Key: "times", Matcher: onlyTimes}},
	}, []*fakeMock{m, used, only}, in)

	require.Zero(t, scenario.runs, "the report runs no matcher that keeps state")
	require.Equal(t, 1, usedUp.runs, "matching ran it, the report not again")
	require.Equal(t, 1, onlyTimes.runs, "matching ran it, the report not again")
	require.Equal(t, []vlatte.NearMiss{
		{MockID: "used", Name: "used", Passed: 2, Total: 3, Failed: []string{"times"}},
		{MockID: "only", Name: "only", Passed: 0, Total: 1, Failed: []string{"times"}},
		{MockID: "m", Name: "m", Passed: 1, Total: 2, Failed: []string{"path"}, Skipped: []string{"scenario"}},
	}, got)
}

func TestNearMisses_RunsNoMatcherThatClaimsATurn(t *testing.T) {
	// A scenario's matcher takes its turn only when its mock is found, but its match may still start the scenario: the
	// report runs none, and names it.
	turn := &turnMatcher{}
	m := newFakeMockWith("m", 0)

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		m: {part("method", match.Equal("GET")), part("path", match.Equal("/b")), {Key: "scenario", Matcher: turn}},
	}, []*fakeMock{m}, request{"method": "GET", "path": "/a"})

	require.Zero(t, turn.matches)
	require.Equal(t, []vlatte.NearMiss{
		{MockID: "m", Name: "m", Passed: 1, Total: 2, Failed: []string{"path"}, Skipped: []string{"scenario"}},
	}, got)
}

func TestNearMisses_RanksByThePartsOfTheRequest(t *testing.T) {
	// A test that sent the request a mock wants, to a mock used up, needs to read that mock first, ahead of one
	// that matched more parts but wants another request. A part that keeps state and passed is no part of the
	// request either: a mock that matched nothing else is no hint.
	in := request{"method": "GET", "path": "/a", "h": "1", "q": "1"}
	a, b, c := newFakeMockWith("a", 0), newFakeMockWith("b", 0), newFakeMockWith("c", 0)

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		a: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), part("h", match.Equal("1")), part("q", match.Equal("2"))},
		b: {part("method", match.Equal("GET")), part("path", match.Equal("/a")), {Key: "times", Matcher: &statefulMatcher{}}},
		c: {{Key: "times", Matcher: &statefulMatcher{matches: true}}, part("method", match.Equal("POST"))},
	}, []*fakeMock{a, b, c}, in)

	require.Equal(t, []vlatte.NearMiss{
		{MockID: "b", Name: "b", Passed: 2, Total: 3, Failed: []string{"times"}},
		{MockID: "a", Name: "a", Passed: 3, Total: 4, Failed: []string{"q"}},
	}, got)
}

func TestNearMisses_AFailingMatcherFailsItsPart(t *testing.T) {
	// A matcher that fails, or panics, as the report runs it, is a part that didn't match, with its error: the
	// report goes on with the next ones.
	in := request{"method": "GET"}
	m := newFakeMockWith("m", 0)

	failing := vlatte.MatcherFunc("failing", func(vlatte.MatcherArg) (bool, error) { return false, errors.New("bad") })
	panicking := vlatte.MatcherFunc("panicking", func(vlatte.MatcherArg) (bool, error) { panic("boom") })

	got := nearMisses(t, map[*fakeMock][]*vlatte.Eval[request]{
		m: {part("method", match.Equal("POST")), part("a", failing), part("b", panicking), part("x", match.Equal(""))},
	}, []*fakeMock{m}, in)

	require.Len(t, got, 1)
	require.Equal(t, 1, got[0].Passed)
	require.Equal(t, 4, got[0].Total)
	require.Len(t, got[0].Failed, 3)
	require.Equal(t, "method", got[0].Failed[0])
	require.Equal(t, "a: bad", got[0].Failed[1], "an error names itself: a broken matcher isn't a request that differs")
	require.Contains(t, got[0].Failed[2], "b: ")
	require.Contains(t, got[0].Failed[2], "boom")
}

func TestFind_KeepingMissesFindsTheSameMock(t *testing.T) {
	// Keeping the misses changes no match: Find stops at the first mock that matches, either way.
	in := request{"method": "GET"}
	a, b, c := newFakeMockWith("a", 0), newFakeMockWith("b", 0), newFakeMockWith("c", 0)
	var cRuns int

	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		a: {part("method", match.Equal("POST"))},
		b: {part("method", match.Equal("GET"))},
		c: {part("method", counted(match.Equal("GET"), &cRuns))},
	}
	evals := func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] }
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a, b, c}))

	for _, misses := range []*vlatte.Misses[*fakeMock, request]{nil, new(vlatte.Misses[*fakeMock, request])} {
		found, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), in, evals, misses)
		require.NoError(t, err)
		require.True(t, matched)
		require.Same(t, b, found)
	}

	require.Zero(t, cRuns)
}

func TestFind_AReusedMissesHoldsItsLastRequest(t *testing.T) {
	// A server reuses its misses, a request at a time, so a matched request costs no room: each Find starts them
	// over, so a report never names the mocks of a request before, nor of one a mock matched.
	a, b := newFakeMockWith("a", 0), newFakeMockWith("b", 0)
	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		a: {part("path", match.Equal("/b")), part("method", match.Equal("GET"))},
		b: {part("method", match.Equal("POST")), part("path", match.Equal("/b"))},
	}
	evals := func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] }
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a, b}))
	describe := func(e *vlatte.Eval[request]) string { return e.Key }
	misses := new(vlatte.Misses[*fakeMock, request])

	_, matched, err := seq.Find(t.Context(), nil, request{"method": "GET"}, evals, misses)
	require.NoError(t, err)
	require.False(t, matched)

	_, matched, err = seq.Find(t.Context(), nil, request{"path": "/b"}, evals, misses)
	require.NoError(t, err)
	require.False(t, matched)
	require.Equal(t, []vlatte.NearMiss{
		{MockID: "a", Name: "a", Passed: 1, Total: 2, Failed: []string{"method"}},
		{MockID: "b", Name: "b", Passed: 1, Total: 2, Failed: []string{"method"}},
	}, misses.NearMisses(t.Context(), describe), "the mocks of the request before are gone")

	_, matched, err = seq.Find(t.Context(), nil, request{"method": "POST", "path": "/b"}, evals, misses)
	require.NoError(t, err)
	require.True(t, matched)
	require.Empty(t, misses.NearMisses(t.Context(), describe), "a matched request has no misses")

	misses.Reset()
	require.Empty(t, misses.NearMisses(t.Context(), describe))
}

func TestNearMisses_AClientThatLeftStillGetsTheWholeReport(t *testing.T) {
	// The report is the request's answer and its journal entry: a client that leaves as it is built, or before,
	// changes neither. Every mock matching tried is ranked, and a matcher that reads the request's context sees one
	// that goes on.
	in := request{"method": "GET", "path": "/a"}
	a, b := newFakeMockWith("a", 0), newFakeMockWith("b", 0)

	alive := vlatte.MatcherFunc("alive", func(arg vlatte.MatcherArg) (bool, error) { return true, arg.Context().Err() })
	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		a: {part("method", match.Equal("POST")), part("path", alive)},
		b: {part("path", match.Equal("/b")), part("method", match.Equal("GET"))},
	}
	evals := func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] }
	misses := new(vlatte.Misses[*fakeMock, request])

	_, matched, err := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a, b})).Find(t.Context(), nil, in, evals, misses)
	require.NoError(t, err)
	require.False(t, matched)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.Equal(t, []vlatte.NearMiss{
		{MockID: "a", Name: "a", Passed: 1, Total: 2, Failed: []string{"method"}},
		{MockID: "b", Name: "b", Passed: 1, Total: 2, Failed: []string{"path"}},
	}, misses.NearMisses(ctx, func(e *vlatte.Eval[request]) string { return e.Key }))
}
