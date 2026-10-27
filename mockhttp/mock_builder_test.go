package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest"
)

// _idOracle is the ID rule written independently of checkMockID: the characters a URL path needs no escaping for.
var _idOracle = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

func TestBuilder_ID(t *testing.T) {
	m := MustNew(Setup().Silent())

	mock, err := Getf("/users").ID("users-get").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "users-get", mock.ID())

	_, err = Getf("/users").ID("users/get").Reply(OK()).Build(m)
	require.ErrorIs(t, err, errMockID)
	require.EqualError(t, err, `mock: id "users/get": an id holds only letters, digits, '.', '_', '~' and '-'`)

	generated, err := Getf("/users").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Regexp(t, _idOracle, generated.ID(), "generated IDs follow the rule too")
}

func TestPropBuilder_IDRule(t *testing.T) {
	// M7: an ID is accepted iff it follows the rule, and it is kept exactly as given.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.OneOf(rapid.StringMatching(`[A-Za-z0-9._~-]{0,12}`), rapid.String()).Draw(t, "id")

		mock, err := Getf("/x").ID(id).Reply(OK()).Build(m)
		if _idOracle.MatchString(id) {
			require.NoError(t, err)
			require.Equal(t, id, mock.ID())
		} else {
			require.ErrorIs(t, err, errMockID)
		}
	})
}

// mapsOnly is an on_complete listener whose Prepare takes the parameters as mock files write them, a map, and returns a
// value of its own, as the webhook does: given what it returned, it fails.
type mapsOnly struct{}

func (mapsOnly) Prepare(cfg vlatte.EventConfig, _ vlatte.TemplateEngine) (vlatte.Parameters, error) {
	if _, ok := cfg.Parameters.(map[string]any); !ok {
		return nil, fmt.Errorf("maps_only: parameters are %T, not a map", cfg.Parameters)
	}

	return "prepared", nil
}

func (mapsOnly) Run(context.Context, vlatte.OnComplete) error { return nil }

func TestBuilder_OneBuilderOnTwoServers(t *testing.T) {
	// #23: Build returned the builder's own mock and wrote the prepared event parameters back into the builder, so a
	// second server served the very same mock, and its listener's Prepare got the first build's output. Each build is
	// a new mock now, and the builder stays as it was written.
	newServer := func() *Server {
		s := MustNew(Setup().Silent().OnCompleteListener("audit", mapsOnly{}))
		s.MustStart()
		t.Cleanup(s.Close)

		return s
	}

	written := map[string]any{"url": "http://audit.test"}
	b := Getf("/orders").OnCompleteEvent(vlatte.EventConfig{Name: "audit", Parameters: written}).Once().Reply(OK())

	s1, s2 := newServer(), newServer()
	first := s1.MustMock(b).Mocks()[0]
	second := s2.MustMock(b).Mocks()[0]

	require.NotSame(t, first, second)
	require.NotEqual(t, first.ID(), second.ID())
	require.Equal(t, written, b.spec.events[0].Parameters, "the builder keeps what was written")
	require.Equal(t, "prepared", second.Events().OnComplete[0].Parameters, "each mock has its own prepared parameters")
	require.Len(t, b.spec.expectations, 2, "path and method: Once and building add nothing to the builder")

	status, _ := get(t, s1, "/orders")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, int64(1), first.Hits())
	require.Zero(t, second.Hits(), "hits are per mock")

	// Each mock counts its own Once.
	status, _ = get(t, s2, "/orders")
	require.Equal(t, http.StatusOK, status, "s2 serves its one request")

	status, _ = get(t, s1, "/orders")
	require.Equal(t, StatusNoMatch, status, "s1 served its one request")
}

func TestBuilder_ACallbackNeedsNoRegisteredListener(t *testing.T) {
	// A mock's events must name a registered listener; a callback carries its own, so a server with none, or with no
	// listener store, builds it. A callback added to a builder later doesn't reach the mocks it built already.
	for name, setup := range map[string]*ConfigBuilder{
		"no listener":       Setup(),
		"no listener store": Setup().EventListeners(nil),
	} {
		t.Run(name, func(t *testing.T) {
			var first, later atomic.Int32

			m := MustNew(setup.Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			b := Getf("/x").Callback(func(context.Context, vlatte.OnComplete) error {
				first.Add(1)
				return nil
			}).Reply(OK())

			_, err := m.Mock(b)
			require.NoError(t, err)

			b.Callback(func(context.Context, vlatte.OnComplete) error {
				later.Add(1)
				return nil
			})

			status, _ := get(t, m, "/x")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, int32(1), first.Load())
			require.Zero(t, later.Load(), "the mock built before the callback was added runs only its own")
		})
	}
}

func TestBuilder_EachMockCountsItsOwnTimes(t *testing.T) {
	// Times was a matcher the builder held, so every mock built from the builder shared one count: a Once builder,
	// served and then remocked, made a mock that never matched, while its hits said 0. Each mock counts its own now.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	b := Getf("/once").Once().Reply(OK())
	scope := m.MustMock(b)

	status, _ := get(t, m, "/once")
	require.Equal(t, http.StatusOK, status)

	status, _ = get(t, m, "/once")
	require.Equal(t, StatusNoMatch, status, "served once")

	_, err := m.Remock(t.Context(), scope, b)
	require.NoError(t, err)

	status, _ = get(t, m, "/once")
	require.Equal(t, http.StatusOK, status, "the new mock counts from zero")
}

func TestBuilder_Times(t *testing.T) {
	// Times is a setting of the builder, as Name is: it takes 1 or more, as repeat does in mock files, and the last
	// call wins. As matchers, Times(0) made a mock that never matched, and two calls both applied.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	_, err := Getf("/never").Times(0).Reply(OK()).Build(m)
	require.EqualError(t, err, "mock: times 0: expected 1 or more")

	m.MustMock(Getf("/last").Times(2).Times(3).Reply(OK()))

	for i := range 3 {
		status, _ := get(t, m, "/last")
		require.Equal(t, http.StatusOK, status, "request %d", i+1)
	}

	status, _ := get(t, m, "/last")
	require.Equal(t, StatusNoMatch, status, "served 3")
}

func TestBuilder_ScenarioAdvancesPerServer(t *testing.T) {
	// Build binds the scenario matcher to the server's scenario store and adds it to the mock it makes: a builder
	// used on two servers runs the scenario on each, and building adds no matcher to the builder.
	b := Getf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").Reply(OK())

	s1, s2 := MustNew(Setup().Silent()), MustNew(Setup().Silent())
	for _, s := range []*Server{s1, s2} {
		s.MustStart()
		t.Cleanup(s.Close)
		s.MustMock(b)
	}

	require.Len(t, b.spec.expectations, 2, "path and method only")

	status, _ := get(t, s1, "/pay")
	require.Equal(t, http.StatusOK, status)

	status, _ = get(t, s1, "/pay")
	require.Equal(t, StatusNoMatch, status, "the scenario of s1 moved on to paid")

	status, _ = get(t, s2, "/pay")
	require.Equal(t, http.StatusOK, status, "the scenario of s2 is still where it started")
}

func TestBuilder_AScenarioMockWithoutARequiredState(t *testing.T) {
	// A mock that requires no state of its scenario matches in any state, and its new state moves the
	// scenario, one no mock started too: it required "", the state of no scenario, and its new state was dropped.
	newServer := func(builders ...*Builder) *Server {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		for _, b := range builders {
			m.MustMock(b)
		}

		return m
	}

	t.Run("matches in any state", func(t *testing.T) {
		m := newServer(
			Getf("/begin").StartScenario("checkout").ScenarioStateWillBe("cart").Reply(OK()),
			Getf("/pay").ScenarioIs("checkout").ScenarioStateWillBe("paid").Reply(OK()),
			Getf("/cart").ScenarioIs("checkout").ScenarioStateIs("cart").Reply(OK()))

		for _, step := range []struct {
			path string
			want int
		}{
			{"/begin", http.StatusOK},
			{"/pay", http.StatusOK},
			{"/cart", StatusNoMatch},
		} {
			status, _ := get(t, m, step.path)
			require.Equal(t, step.want, status, step.path)
		}
	})

	t.Run("starts the scenario it moves", func(t *testing.T) {
		m := newServer(
			Getf("/pay").ScenarioIs("checkout").ScenarioStateWillBe("paid").Reply(OK()),
			Getf("/cart").ScenarioIs("checkout").ScenarioStateIs("cart").Reply(OK()))

		status, _ := get(t, m, "/pay")
		require.Equal(t, http.StatusOK, status)

		// A scenario no mock started matches whatever state a mock requires: this one was started, at paid.
		status, _ = get(t, m, "/cart")
		require.Equal(t, StatusNoMatch, status)
	})
}

func TestBuilder_AddedTwiceToOneServer(t *testing.T) {
	// A builder without an ID gets a new one at each build, so it can be added to a server more than once. With an
	// ID, a server still holds one mock per ID.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	b := Getf("/twice").Reply(OK())
	scope := m.MustMock(b, b)
	require.Len(t, scope.IDs(), 2)

	named := Getf("/named").ID("named").Reply(OK())
	_, err := m.Mock(named, named)
	require.ErrorIs(t, err, vlatte.ErrDuplicateID)

	status, _ := get(t, m, "/twice")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, int64(1), scope.Hits())

	// Remocking with the same builder serves a new mock, from zero hits.
	scope, err = m.Remock(t.Context(), scope, b)
	require.NoError(t, err)
	require.Zero(t, scope.Hits())
}

func TestBuilder_ChangesAfterMockReachOnlyLaterMocks(t *testing.T) {
	// A served mock is the builder as it was when the mock was built: what the builder gets afterwards goes only into
	// the mocks built afterwards. Before, the builder held the served mock, so Enabled(false) disabled it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	b := Getf("/v").Reply(OK())
	served := m.MustMock(b).Mocks()[0]

	b.Enabled(false).Header("X-Later", match.Eq("yes"))

	require.True(t, served.Enabled())
	status, _ := get(t, m, "/v")
	require.Equal(t, http.StatusOK, status, "the served mock has no X-Later matcher")

	later := m.MustMock(b).Mocks()[0]
	require.False(t, later.Enabled())
	require.Len(t, later.spec.expectations, 3)
}

func TestReply_BodyFileDir(t *testing.T) {
	// A relative body file opens from the reply's dir; an absolute one, and one without a dir, as given.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "body.txt"), []byte("from dir"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "users.txt"), []byte("templated"), 0o600))

	inDir := func(rep *Reply) *Reply {
		rep.bodyFileDir = dir
		return rep
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/relative").Reply(inDir(OK().BodyFile("body.txt"))),
		Getf("/absolute").Reply(inDir(OK().BodyFile(filepath.Join(dir, "sub", "users.txt")))),
		Getf("/users").Reply(inDir(OK().BodyFile("sub/{{ index .Request.URLPathSegments 0 }}.txt"))),
		Getf("/working-dir").Reply(OK().BodyFile("testdata/loader/02.txt")),
	)

	for path, want := range map[string]string{
		"/relative":    "from dir",
		"/absolute":    "templated",
		"/users":       "templated",
		"/working-dir": "test ok\n",
	} {
		res, err := http.Get(m.URL(path))
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, want, string(b), path)
	}
}

func TestReply_TemplatedBodyKeepsItsBytes(t *testing.T) {
	// Found by TestPropMockFile_ServesItsModel: a templated body pointed into a pooled buffer that went back
	// to the pool before the response rendered, so gzip, which takes a pooled buffer too, wrote over it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/gzip").Reply(OK().BodyTemplate("{{ .Request.Method }} templated").Gzip()),
		Getf("/file").Reply(OK().BodyFileWithTemplate("testdata/loader/02.txt").Gzip()),
	)

	for path, want := range map[string]string{"/gzip": "GET templated", "/file": "test ok\n"} {
		for range 20 {
			res, err := http.Get(m.URL(path))
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, want, string(b), path)
		}
	}
}

func TestBuilder_BuildsAtTheSameTimeOnManyServers(t *testing.T) {
	// Building only reads the builder and its reply, so one builder can be built for many servers at once.
	b := Getf("/same").
		StartScenario("concurrent").
		Reply(OK().BodyTemplate(`{{ .Request.Method }}`).HeaderTemplate("X-Id", `{{ .Request.ID }}`).Gzip())

	servers := make([]*Server, 4)
	for i := range servers {
		servers[i] = MustNew(Setup().Silent())
		servers[i].MustStart()
		t.Cleanup(servers[i].Close)
	}

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Go(func() {
			_, err := s.Mock(b)
			assert.NoError(t, err)
		})
	}

	wg.Wait()

	for _, s := range servers {
		status, body := get(t, s, "/same")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "GET", body)
	}
}

func TestBuilder_DelayFunc(t *testing.T) {
	// DelayFunc picks the delay before each response, so a delay such as Jitter varies from one response to the next.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	var picks atomic.Int64
	m.MustMock(Getf("/d").
		DelayFunc(func() time.Duration {
			picks.Add(1)
			return time.Millisecond
		}).
		Reply(OK()))

	for range 2 {
		status, _ := get(t, m, "/d")
		require.Equal(t, http.StatusOK, status)
	}

	require.Equal(t, int64(2), picks.Load())
}

func TestBuilder_MatchesGivesTheMatcherTheRequest(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/m").Matches(match.Function(func(v any) (bool, error) {
		r, ok := v.(*http.Request)
		return ok && r.URL.Query().Get("ok") == "1", nil
	})).Reply(OK()))

	status, _ := get(t, m, "/m?ok=1")
	require.Equal(t, http.StatusOK, status)

	status, _ = get(t, m, "/m?ok=0")
	require.Equal(t, StatusNoMatch, status)
}

func TestBuilder_MatchesDescribesUnderCustom(t *testing.T) {
	// A matcher of the whole request that describes itself, such as a custom one of the server's registry, loads back
	// from the mock's description: a predicate of RequestMatches can't.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/m").ID("m").
		Matches(match.Not(match.Nil())).
		RequestMatches(func(*http.Request) (bool, error) { return true, nil }).
		Reply(OK()))

	desc, ok := scope.Mock("m").Describe().Props.(map[string]any)
	require.True(t, ok)

	request, ok := desc["request"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{descriptor.SingleArg("not", descriptor.KeyOnly("nil"))}, request["custom"])
}

// scenarioServer runs the scenario suite on a server whose mocks are built in Go, or from mock files.
type scenarioServer struct {
	m         *Server
	fromFiles bool
}

var _ scenariotest.Server = (*scenarioServer)(nil)

func newScenarioServer(fromFiles bool) func(t *testing.T) scenariotest.Server {
	return func(t *testing.T) scenariotest.Server {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		return &scenarioServer{m: m, fromFiles: fromFiles}
	}
}

func (s *scenarioServer) Mock(t *testing.T, mocks ...scenariotest.Mock) {
	for _, mk := range mocks {
		if s.fromFiles {
			doc, err := json.Marshal(map[string]any{
				"scenario": map[string]string{
					"name": mk.Scenario, "required_state": mk.RequiredState, "new_state": mk.NewState,
				},
				"request":  map[string]string{"method": http.MethodGet, "path": "/scenario"},
				"response": map[string]any{"status": http.StatusOK, "body": mk.ID},
			})
			require.NoError(t, err)

			s.m.MustMock(FromBytes(doc, "json"))

			continue
		}

		b := Getf("/scenario").ScenarioIs(mk.Scenario).Reply(OK().PlainText(mk.ID))
		if mk.RequiredState != "" {
			b.ScenarioStateIs(mk.RequiredState)
		}

		if mk.NewState != "" {
			b.ScenarioStateWillBe(mk.NewState)
		}

		s.m.MustMock(b)
	}
}

func (s *scenarioServer) Call(t *testing.T) string {
	res, body := do(t, http.MethodGet, s.m.URL("/scenario"), nil, nil)

	switch res.StatusCode {
	case http.StatusOK:
		return string(body)
	case StatusNoMatch:
		return ""
	default:
		t.Fatalf("want a mock's answer or no match, got %d: %s", res.StatusCode, body)
		return ""
	}
}

func (s *scenarioServer) Store() *matchscenario.Store { return s.m.scenarioStore }

func TestBuilder_ServesScenarios(t *testing.T) {
	scenariotest.Run(t, newScenarioServer(false))
}

func TestBuilder_AScenarioMovesWhenItsClientLeavesDuringTheDelay(t *testing.T) {
	// The scenario moves when its mock is picked: a client that gives up during the delay and calls again gets the
	// mock of the next state, as another client would have.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	waiting := make(chan struct{})
	m.MustMock(
		Getf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").
			DelayFunc(func() time.Duration {
				close(waiting)
				return time.Hour
			}).
			Reply(OK()),
		Getf("/pay").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(Status(http.StatusConflict)),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/pay"), nil)
	require.NoError(t, err)

	called := make(chan error, 1)
	go func() {
		res, err := _rawClient.Do(req)
		if err == nil {
			res.Body.Close()
		}

		called <- err
	}()

	<-waiting
	cancel()
	require.ErrorIs(t, <-called, context.Canceled)

	res, _ := do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusConflict, res.StatusCode)
}
