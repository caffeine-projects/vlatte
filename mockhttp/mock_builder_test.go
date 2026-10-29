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
	"github.com/caffeine-projects/vlatte/match"
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
	// A mock that requires no state of its scenario matches in any state, and its new state moves the scenario, one
	// nothing moved yet too.
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

	t.Run("moves the scenario", func(t *testing.T) {
		m := newServer(
			Getf("/pay").ScenarioIs("checkout").ScenarioStateWillBe("paid").Reply(OK()),
			Getf("/cart").ScenarioIs("checkout").ScenarioStateIs("cart").Reply(OK()))

		status, _ := get(t, m, "/pay")
		require.Equal(t, http.StatusOK, status)

		// In paid, the mock requiring cart doesn't match.
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
	require.Equal(t, []any{vlatte.DescribeSingleArg("not", vlatte.DescribeKeyOnly("nil"))}, request["custom"])
}

// scenarioServer runs the scenario suite on a server whose mocks are built in Go, or from mock files.
type scenarioServer struct {
	*Server

	fromFiles bool
}

var _ scenariotest.Server = (*scenarioServer)(nil)

func newScenarioServer(fromFiles bool) func(t *testing.T) scenariotest.Server {
	return func(t *testing.T) scenariotest.Server {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		return &scenarioServer{Server: m, fromFiles: fromFiles}
	}
}

// _scenarioKey is the scenario key of the keyed mocks of a scenarioServer: the header CallWithKey sends.
const _scenarioKey = `{{ .Request.Header.Get "X-Scenario-Key" }}`

func (s *scenarioServer) Mock(t *testing.T, mocks ...scenariotest.Mock) {
	for _, mk := range mocks {
		var key string
		if mk.Keyed {
			key = _scenarioKey
		}

		if s.fromFiles {
			doc, err := json.Marshal(map[string]any{
				"id":      mk.ID,
				"enabled": !mk.Disabled,
				"scenario": map[string]string{
					"name": mk.Scenario, "required_state": mk.RequiredState, "new_state": mk.NewState, "key": key,
				},
				"request":  map[string]string{"method": http.MethodGet, "path": "/scenario"},
				"response": map[string]any{"status": http.StatusOK, "body": mk.ID},
			})
			require.NoError(t, err)

			s.MustMock(FromBytes(doc, "json"))

			continue
		}

		b := Getf("/scenario").ID(mk.ID).Enabled(!mk.Disabled).ScenarioIs(mk.Scenario).Reply(OK().PlainText(mk.ID))
		if mk.RequiredState != "" {
			b.ScenarioStateIs(mk.RequiredState)
		}

		if mk.NewState != "" {
			b.ScenarioStateWillBe(mk.NewState)
		}

		if key != "" {
			b.ScenarioKey(key)
		}

		s.MustMock(b)
	}
}

func (s *scenarioServer) Call(t *testing.T) string {
	return s.call(t, nil)
}

func (s *scenarioServer) CallWithKey(t *testing.T, key string) string {
	return s.call(t, http.Header{"X-Scenario-Key": {key}})
}

// call sends the request of the mocks with header.
func (s *scenarioServer) call(t *testing.T, header http.Header) string {
	res, body := do(t, http.MethodGet, s.URL("/scenario"), header, nil)

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

// statusOf sends req and returns the status of the response.
func statusOf(t *testing.T, req *http.Request) int {
	t.Helper()

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode
}

func TestBuilder_HeadersReadsEveryValue(t *testing.T) {
	// Header reads the first value of a header only: a request sending X-Tag twice could match only on its first, and
	// one sending it empty looked like one without it. Headers reads every value, in order, and nil when absent.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/second").Headers("X-Tag", match.Some(match.Equal("b"))).Reply(OK()))
	m.MustMock(Getf("/ordered").Headers("X-Tag", match.Equal([]string{"a", "b"})).Reply(OK()))
	m.MustMock(Getf("/absent").Headers("X-Tag", match.Not(match.Present())).Reply(OK()))

	tests := []struct {
		name   string
		path   string
		values []string // the X-Tag lines of the request; nil sends none
		want   int
	}{
		{"a value after the first", "/second", []string{"a", "b"}, http.StatusOK},
		{"only the first value", "/second", []string{"a"}, StatusNoMatch},
		{"values in order", "/ordered", []string{"a", "b"}, http.StatusOK},
		{"values in another order", "/ordered", []string{"b", "a"}, StatusNoMatch},
		{"one line is one value", "/ordered", []string{"a, b"}, StatusNoMatch},
		{"no header", "/absent", nil, http.StatusOK},
		{"a header sent empty", "/absent", []string{""}, StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.values != nil {
				req.Header["X-Tag"] = tt.values
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_FormFieldsReadsEveryValueOfTheBody(t *testing.T) {
	// FormField reads the first value of a field only. FormFields reads every value, and, like FormField, never the
	// query: a ?tag= must not complete a form.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/tags").FormFields("tag", match.Equal([]string{"a", "b"})).Reply(OK()))

	tests := []struct {
		name  string
		query string
		body  string
		want  int
	}{
		{"every value", "", "tag=a&tag=b", http.StatusOK},
		{"a value missing", "", "tag=a", StatusNoMatch},
		{"a value too many", "", "tag=a&tag=b&tag=c", StatusNoMatch},
		{"a value in the query", "?tag=b", "tag=a", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := post(t, m.URL("/tags"+tt.query), MIMEFormURLEncoded, []byte(tt.body))
			require.Equal(t, tt.want, res.StatusCode)
		})
	}
}

func TestBuilder_Cookie(t *testing.T) {
	// A cookie matches by its name, which keeps its case, as browsers keep it: Session isn't session. A request without
	// the cookie isn't one that sends it empty.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/me").Cookie("session", match.Equal("abc")).Reply(OK()))
	m.MustMock(Getf("/empty").Cookie("session", match.Equal("")).Reply(OK()))

	tests := []struct {
		name   string
		path   string
		cookie string // the Cookie header; "" sends none
		want   int
	}{
		{"the cookie", "/me", "session=abc", http.StatusOK},
		{"among others", "/me", "theme=dark; session=abc", http.StatusOK},
		{"another value", "/me", "session=xyz", StatusNoMatch},
		{"another case of its name", "/me", "Session=abc", StatusNoMatch},
		{"no cookie", "/me", "", StatusNoMatch},
		{"an empty cookie", "/empty", "session=", http.StatusOK},
		{"no cookie for an empty one", "/empty", "", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.cookie != "" {
				req.Header.Set(HeaderCookie, tt.cookie)
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_BasicAuth(t *testing.T) {
	// Basic credentials match as the request sends them, and a request without them matches no credentials, empty
	// ones included: a mock for an empty user must not answer anonymous requests.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/admin").BasicAuth("jeff", "s3cret").Reply(OK()))
	m.MustMock(Getf("/empty").BasicAuth("", "").Reply(OK()))
	m.MustMock(Getf("/anonymous").BasicAuthMatches(match.Not(match.Present()), match.Anything()).Reply(OK()))

	tests := []struct {
		name string
		path string
		auth func(r *http.Request)
		want int
	}{
		{"the credentials", "/admin", func(r *http.Request) { r.SetBasicAuth("jeff", "s3cret") }, http.StatusOK},
		{"another password", "/admin", func(r *http.Request) { r.SetBasicAuth("jeff", "S3cret") }, StatusNoMatch},
		{"a bearer token", "/admin", func(r *http.Request) { r.Header.Set(HeaderAuthorization, "Bearer jeff:s3cret") }, StatusNoMatch},
		{"no credentials", "/admin", func(*http.Request) {}, StatusNoMatch},
		{"empty credentials", "/empty", func(r *http.Request) { r.SetBasicAuth("", "") }, http.StatusOK},
		{"no credentials for empty ones", "/empty", func(*http.Request) {}, StatusNoMatch},
		{"no credentials when none are wanted", "/anonymous", func(*http.Request) {}, http.StatusOK},
		{"credentials when none are wanted", "/anonymous", func(r *http.Request) { r.SetBasicAuth("jeff", "s3cret") }, StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)
			tt.auth(req)

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_Host(t *testing.T) {
	// The host is the one the request names, without its port, which the client picks: a mock for api.local answers
	// api.local:8080 and API.local, as host names ignore case.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Host(match.EqualIgnoreCase("api.local")).Reply(OK()))
	m.MustMock(Getf("/v6").Host(match.Equal("::1")).Reply(OK()))

	tests := []struct {
		name string
		path string
		host string
		want int
	}{
		{"the host", "/x", "api.local", http.StatusOK},
		{"with a port", "/x", "api.local:8080", http.StatusOK},
		{"in another case", "/x", "API.local", http.StatusOK},
		{"another host", "/x", "api.local.evil", StatusNoMatch},
		{"the server's own", "/x", "", StatusNoMatch},
		{"an IPv6 address, without its brackets", "/v6", "[::1]:8080", http.StatusOK},
		// net/http takes any port, as the host file key's check does: a port that isn't a number is still a port.
		{"with a port that isn't a number", "/x", "api.local:bad", http.StatusOK},
		{"an IPv6 address sent without brackets", "/v6", "::1", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.host != "" {
				req.Host = tt.host
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestForwardProxy_HostMatchesTheHostTheClientAskedFor(t *testing.T) {
	// A forward proxy gets requests in absolute form: Host matches the host they name, so a mock can answer one host
	// and leave the others to the proxy.
	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/users").Host(match.EqualIgnoreCase("api.example.com")).Reply(OK().PlainText("mocked")))

	client := proxyClient(t, m)
	body := func(target string) string {
		res, err := client.Get(target)
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return string(b)
	}

	require.Equal(t, "mocked", body("http://api.example.com/users"))
	// Nothing listens on port 1: the proxy fails to send it on, fast.
	require.NotEqual(t, "mocked", body("http://127.0.0.1:1/users"), "another host goes on to that host")
}

func TestBuilder_ClientIP(t *testing.T) {
	// The client's address is the connection's: an X-Forwarded-For header, which any client can send, doesn't change
	// it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	// httptest listens on 127.0.0.1, or on ::1 where there is no IPv4.
	m.MustMock(Getf("/local").ClientIP(match.IsIn([]string{"127.0.0.1", "::1"})).Reply(OK()))
	m.MustMock(Getf("/office").ClientIP(match.Equal("10.0.0.1")).Reply(OK()))

	send := func(path, forwardedFor string) int {
		req, err := http.NewRequest(http.MethodGet, m.URL(path), nil)
		require.NoError(t, err)

		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}

		return statusOf(t, req)
	}

	require.Equal(t, http.StatusOK, send("/local", ""))
	require.Equal(t, StatusNoMatch, send("/office", ""))
	require.Equal(t, StatusNoMatch, send("/office", "10.0.0.1"), "X-Forwarded-For isn't the client's address")
}

func TestMockFile_HostTakesAnIPv6Address(t *testing.T) {
	// host refuses text with a port or brackets, which would never match: an IPv6 address alone has colons, and is
	// neither.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request: {path: /v6, host: '::1'}\n"), "yaml"))

	req, err := http.NewRequest(http.MethodGet, m.URL("/v6"), nil)
	require.NoError(t, err)
	req.Host = "[::1]:8080"

	require.Equal(t, http.StatusOK, statusOf(t, req))
}

func TestBuilder_URLPathTemplate(t *testing.T) {
	// A path template matches paths of its shape, and PathParam the value of a parameter, whichever was set first.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Request().Method(http.MethodGet).
		PathParam("id", match.Equal("7")).
		URLPathTemplate("/users/{id}/orders/{orderId}").
		PathParam("orderId", match.HasPrefix("BR-")).
		Reply(OK()))
	m.MustMock(Request().Method(http.MethodGet).URLPathTemplate("/files/{name}").Reply(OK()))

	tests := []struct {
		path string
		want int
	}{
		{"/files/a.txt", http.StatusOK},
		{"/files", StatusNoMatch},
		{"/files/", StatusNoMatch},
		{"/users/7/orders/BR-1", http.StatusOK},
		{"/users/8/orders/BR-1", StatusNoMatch},
		{"/users/7/orders/US-1", StatusNoMatch},
		{"/users/7/orders", StatusNoMatch},
		{"/users/7/orders/BR-1/", StatusNoMatch},
		{"/users/7/orders/BR-1/items", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, _ := get(t, m, tt.path)
			require.Equal(t, tt.want, status)
		})
	}
}

func TestBuilder_PathTemplateErrors(t *testing.T) {
	// A path parameter needs a template that has it, and a mock has one template: anything else fails the mock,
	// rather than a matcher that never matches.
	m := MustNew(Setup().Silent())

	tests := []struct {
		name    string
		give    *Builder
		wantErr string
	}{
		{
			"no template", Request().Method(http.MethodGet).PathParam("id", match.Anything()),
			`mock: path param "id": the mock has no path template: set one with URLPathTemplate`,
		},
		{
			"a parameter the template doesn't have", Request().URLPathTemplate("/users/{id}").PathParam("userId", match.Anything()),
			`mock: path param "userId": path template "/users/{id}" has no such parameter`,
		},
		{
			"a second template", Request().URLPathTemplate("/a/{id}").URLPathTemplate("/b/{id}"),
			`mock: path template "/b/{id}": the mock has path template "/a/{id}" already`,
		},
		{
			"a template that doesn't parse", Request().URLPathTemplate("/users/a{id}"),
			`mock: path template "/users/a{id}": segment "a{id}": a parameter is a whole segment, such as {id}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.give.Reply(OK()).Build(m)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestBuilder_ContentTypeIgnoresParameters(t *testing.T) {
	// D5: ContentType compared the whole header, and a multipart Content-Type always carries a boundary, so
	// ContentType("multipart/form-data") never matched. A value without parameters matches the media type alone.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/multipart").ContentType(MIMEMultipartForm).Reply(OK()))
	m.MustMock(Postf("/json").ContentType(MIMEApplicationJSON).Reply(OK()))
	m.MustMock(Postf("/charset").ContentType("text/plain; charset=utf-8").Reply(OK()))

	multipartType, multipartBody := formModel{fields: []formField{{name: "a", value: "1"}}}.encode(t)

	tests := []struct {
		path        string
		contentType string
		status      int
	}{
		{"/multipart", multipartType, http.StatusOK},
		{"/json", "Application/JSON; charset=utf-8", http.StatusOK},
		{"/json", " application/json ", http.StatusOK},
		{"/json", "application/jsonx", StatusNoMatch},
		{"/json", "", StatusNoMatch},
		{"/charset", "TEXT/plain; charset=UTF-8", http.StatusOK},
		{"/charset", "text/plain", StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.path+" "+tc.contentType, func(t *testing.T) {
			res, _ := post(t, m.URL(tc.path), tc.contentType, multipartBody)
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}
