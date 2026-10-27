package mockhttp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestNoMatch_ListsTheClosestMocks(t *testing.T) {
	// A request no mock matched is the commonest puzzle of a test: the answer names the mocks that came closest,
	// and the parts of the request that differ, so the fix is read off the answer.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/users").ID("users").Name("list users").Header("Accept", match.Equal("application/json")).Reply(OK()),
		Postf("/users").ID("create").Reply(Created()),
		Getf("/orders").ID("orders").Query("page", match.Equal("2")).Reply(OK()),
	)

	status, header, body := getFull(t, m, "/users")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
	require.Equal(t, "REQUEST DID NOT MATCH\nGET "+m.URL("/users")+"\n\n"+
		"CLOSEST MOCKS\n"+
		"[users] list users: 2 of 3 parts matched; differs in header Accept\n"+
		"[create]: 1 of 2 parts matched; differs in method\n"+
		"[orders]: 1 of 3 parts matched; differs in path, query page\n", body)
}

func TestNoMatch_NearMissesRunNoMatcherTwice(t *testing.T) {
	// The report goes on from where matching stopped: a matcher with an action, such as Peek's, runs once for a
	// request no mock matched, as it does for one a mock matched.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	var runs int
	peek := match.Peek(match.Equal("1"), func(any) error { runs++; return nil })

	m.MustMock(
		Getf("/a").ID("a").Header("x", peek).Reply(OK()),
		Postf("/b").ID("b").Header("x", peek).Reply(OK()),
	)

	status, _, body := getFull(t, m, "/a")
	require.Equal(t, StatusNoMatch, status)
	require.Contains(t, body, "[a]: 2 of 3 parts matched; differs in header x\n")
	require.Equal(t, 2, runs, "the header of each mock ran once: a's in matching, b's in the report")
}

func TestNoMatch_NearMissesSayWhereTheScenarioIs(t *testing.T) {
	// A request that waits on a scenario's state reads why no mock answered: the state the mock needs, and the one the
	// scenario is in. The report runs the scenario's matcher, which moves nothing: the next request still finds the
	// scenario where it was.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("a").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(OK()),
		Getf("/b").ID("b").StartScenario("checkout").ScenarioStateWillBe("paid").Reply(Status(http.StatusAccepted)),
	)

	status, _, body := getFull(t, m, "/a")
	require.Equal(t, StatusNoMatch, status)
	require.Contains(t, body, "[a]: 2 of 3 parts matched; differs in scenario checkout needs paid, is started\n")

	status, _, _ = getFull(t, m, "/b")
	require.Equal(t, http.StatusAccepted, status, "the scenario is still where it started")
}

func TestNoMatch_NearMissesTurnOff(t *testing.T) {
	// The report runs the matchers matching never reached: a server with many mocks, or matchers with actions, turns
	// it off, in code or in its settings.
	for name, setup := range map[string]*ConfigBuilder{
		"builder": Setup().Silent().NoNearMisses(),
		"setting": Setup().Silent().ConfigSources(ConfigArgs([]string{"--near-misses=false"})),
	} {
		t.Run(name, func(t *testing.T) {
			m := MustNew(setup)
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/a").Reply(OK()))

			status, _, body := getFull(t, m, "/b")
			require.Equal(t, StatusNoMatch, status)
			require.Equal(t, "REQUEST DID NOT MATCH\nGET "+m.URL("/b")+"\n\n", body)
		})
	}
}

func TestNoMatch_NamesEachPartAsTheBuilderDoes(t *testing.T) {
	// A part is named as a test wrote it, Times and RequestMatches included, and a matcher's error stays on its
	// mock's line, whatever it holds. A Once used up is the part a mock differs in: matching ran it. Matching stops
	// at the path, before the predicate: only the report runs it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("once").Once().Reply(OK()),
		Getf("/c").ID("predicate").RequestMatches(func(*http.Request) (bool, error) {
			return false, errors.New("bad\nrequest")
		}).Reply(OK()),
	)

	status, _ := get(t, m, "/a")
	require.Equal(t, http.StatusOK, status)

	_, _, body := getFull(t, m, "/a")
	require.Contains(t, body, "\n[once]: 2 of 3 parts matched; differs in times\n")

	_, _, body = getFull(t, m, "/b")
	require.Contains(t, body, "\n[predicate]: 1 of 3 parts matched; differs in path, request: ")
	require.Contains(t, body, "bad request\n")
}

func TestNoMatch_NamesAKeyedPartWithItsKey(t *testing.T) {
	// A part of many keys names the one a mock differs in, as a header does: "differs in cookie" would leave a test
	// to find which cookie.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/a").ID("headers").Headers("X-Id", match.Equal([]string{"1"})).Reply(OK()),
		Getf("/a").ID("cookie").Cookie("session", match.Equal("s")).Reply(OK()),
		Request().ID("param").Method(http.MethodGet).URLPathTemplate("/{id}").PathParam("id", match.Equal("b")).Reply(OK()),
	)

	_, _, body := getFull(t, m, "/a")
	require.Contains(t, body, "\n[headers]: 2 of 3 parts matched; differs in headers X-Id\n")
	require.Contains(t, body, "\n[cookie]: 2 of 3 parts matched; differs in cookie session\n")
	require.Contains(t, body, "\n[param]: 2 of 3 parts matched; differs in path_params id\n")
}
