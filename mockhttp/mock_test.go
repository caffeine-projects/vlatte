package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/match"
)

func TestRace(t *testing.T) {
	m := &Mock{}
	jobs := 10
	wg := sync.WaitGroup{}

	for range jobs {
		wg.Go(func() {
			m.Inc()
			m.Hits()
		})

		m.Inc()
	}

	m.Hits()
	m.Inc()
	m.Inc()

	wg.Wait()

	assert.EqualValues(t, (jobs*2)+2, m.Hits())
}

func TestMock(t *testing.T) {
	m, err := Getf("/mock").Reply(OK()).Build(MustNew(Setup().Silent()))
	require.NoError(t, err)

	t.Run("should init enabled", func(t *testing.T) {
		assert.True(t, m.Enabled())
	})

	t.Run("should disable mock when calling .Disable()", func(t *testing.T) {
		m.Disable()
		assert.False(t, m.Enabled())

		m.Enable()
		assert.True(t, m.Enabled())
	})

	t.Run("should return called when it was hit", func(t *testing.T) {
		assert.False(t, m.Called())
		m.Inc()
		assert.True(t, m.Called())
	})
}

func TestMock_MarshalJSON(t *testing.T) {
	app := MustNew()
	builder := Request().
		Header("accept", match.Eq("test")).
		Once().
		ScenarioIs("test").ScenarioStateIs("state").ScenarioStateWillBe("future").
		Reply(OK())

	m, err := builder.Build(app)
	require.NoError(t, err)

	b, err := m.MarshalJSON()
	require.NoError(t, err)

	fmt.Println(string(b))
}

func TestMock_String(t *testing.T) {
	// Failure bodies and matching errors name a mock with String, so it must read the same as assertion messages.
	m := MustNew(Setup().Silent())

	named, err := Getf("/").ID("a").Name("users").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [a] users", named.String())

	unnamed, err := Getf("/").ID("b").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [b]", unnamed.String())
}

func TestMock_ADescribedMockLoadsBackAndMatchesTheSame(t *testing.T) {
	// The description of a mock is a mock file, as the admin API shows and takes it. Loaded back, it must match the
	// same requests: a matcher of the method must not get lost, nor one of the path or fragment turn into one of the
	// whole URL.
	tests := []struct {
		name        string
		give        *Builder
		wantKeys    []string
		wantMatch   []string
		wantNoMatch []string
	}{
		{
			name:        "path",
			give:        Getf("/test"),
			wantKeys:    []string{"method", "path"},
			wantMatch:   []string{"/test"},
			wantNoMatch: []string{"/other"},
		},
		{
			// The requests are GETs.
			name:        "method",
			give:        Postf("/test"),
			wantKeys:    []string{"method", "path"},
			wantMatch:   []string{},
			wantNoMatch: []string{"/test"},
		},
		{
			name:        "path and fragment",
			give:        Getf("/test").URLFragmentf("contacts"),
			wantKeys:    []string{"fragment", "method", "path"},
			wantMatch:   []string{"/test#contacts"},
			wantNoMatch: []string{"/test#other", "/test"},
		},
		{
			// A relative url names the exact path and query: it describes as requestURI and loads back as it.
			name:        "exact url",
			give:        Request().URL(match.RequestURI("/items?page=2")),
			wantKeys:    []string{"url"},
			wantMatch:   []string{"/items?page=2"},
			wantNoMatch: []string{"/items", "/items?page=2&x=1", "/items?page=3"},
		},
		{
			// Several methods match ignoring case, as one does, and keep doing so loaded back.
			name:        "methods",
			give:        Request().Method("get", "PUT").URLPathf("/x"),
			wantKeys:    []string{"method", "path"},
			wantMatch:   []string{"/x"},
			wantNoMatch: []string{"/y"},
		},
		{
			// A path template describes as written, and its parameters by name.
			name:        "path template",
			give:        Request().URLPathTemplate("/users/{id}").PathParam("id", match.Equal("7")),
			wantKeys:    []string{"path_params", "path_template"},
			wantMatch:   []string{"/users/7"},
			wantNoMatch: []string{"/users/8", "/users/7/", "/users/7/x", "/users"},
		},
		{
			// A path template adds to the other matchers of the path, in Go as in mock files.
			name:        "path and path template",
			give:        Request().URLPathf("/users/*").URLPathTemplate("/{kind}/{id}"),
			wantKeys:    []string{"path", "path_template"},
			wantMatch:   []string{"/users/7"},
			wantNoMatch: []string{"/teams/7", "/users/7/x"},
		},
		{
			// RequestURI is the exact url of mock files in Go.
			name:        "RequestURI",
			give:        Request().RequestURI("/items?page=2&sort=asc"),
			wantKeys:    []string{"url"},
			wantMatch:   []string{"/items?page=2&sort=asc", "/items?sort=asc&page=2"},
			wantNoMatch: []string{"/items?page=2", "/items?page=2&sort=asc&x=1", "/items/?page=2&sort=asc"},
		},
		{
			// A mock file has url or path, not both: the path matcher goes in url.
			name:        "url and path",
			give:        Request().URL(match.Contain("test")).URLPathf("/test"),
			wantKeys:    []string{"url"},
			wantMatch:   []string{"/test"},
			wantNoMatch: []string{"/x/test", "/other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built := MustNew(Setup().Silent())
			built.MustStart()
			t.Cleanup(built.Close)

			desc, err := built.MustMock(tt.give.Reply(OK())).Mocks()[0].MarshalJSON()
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(desc, &doc))
			request, _ := doc["request"].(map[string]any)
			require.Equal(t, tt.wantKeys, slices.Sorted(maps.Keys(request)), string(desc))

			loaded := MustNew(Setup().Silent())
			loaded.MustStart()
			t.Cleanup(loaded.Close)
			loaded.MustMock(FromBytes(desc, "json"))

			for _, target := range tt.wantMatch {
				require.Equal(t, http.StatusOK, getTarget(t, built, target), target)
				require.Equal(t, http.StatusOK, getTarget(t, loaded, target), "loaded: %s: %s", target, desc)
			}

			for _, target := range tt.wantNoMatch {
				require.Equal(t, StatusNoMatch, getTarget(t, built, target), target)
				require.Equal(t, StatusNoMatch, getTarget(t, loaded, target), "loaded: %s: %s", target, desc)
			}
		})
	}
}

func TestMock_ADescribedRequestPartLoadsBackAndMatchesTheSame(t *testing.T) {
	// As TestMock_ADescribedMockLoadsBackAndMatchesTheSame, for the parts of a request beyond its target: each
	// describes under its own key, and loads back to match the request it matched, and miss one that misses that part.
	give := Postf("/x").
		Headers("X-Tag", match.Equal([]string{"a", "b"})).
		FormFields("tag", match.Equal([]string{"a", "b"})).
		Cookie("session", match.Equal("abc")).
		BasicAuth("jeff", "s3cret").
		Host(match.EqualIgnoreCase("api.local")).
		ClientIP(match.Equal("10.0.0.1"))

	// The client's address is the connection's: a middleware sets it from the request, as one behind a proxy may.
	setup := func() *ConfigBuilder {
		return Setup().Silent().Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.RemoteAddr = r.Header.Get("X-Remote-Addr")
				next.ServeHTTP(w, r)
			})
		})
	}

	built := MustNew(setup())
	built.MustStart()
	t.Cleanup(built.Close)

	desc, err := built.MustMock(give.Reply(OK())).Mocks()[0].MarshalJSON()
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(desc, &doc))
	request, _ := doc["request"].(map[string]any)
	require.Equal(t, []string{"basic_auth", "client_ip", "cookie", "forms", "headers", "host", "method", "path"},
		slices.Sorted(maps.Keys(request)), string(desc))

	loaded := MustNew(setup())
	loaded.MustStart()
	t.Cleanup(loaded.Close)
	loaded.MustMock(FromBytes(desc, "json"))

	send := func(m *Server, form string, spoil func(r *http.Request)) int {
		req, err := http.NewRequest(http.MethodPost, m.URL("/x"), strings.NewReader(form))
		require.NoError(t, err)

		req.Host = "api.local"
		req.Header.Set(HeaderContentType, MIMEFormURLEncoded)
		req.Header["X-Tag"] = []string{"a", "b"}
		req.Header.Set(HeaderCookie, "session=abc")
		req.Header.Set("X-Remote-Addr", "10.0.0.1:1234")
		req.SetBasicAuth("jeff", "s3cret")
		spoil(req)

		return statusOf(t, req)
	}

	misses := []struct {
		name  string
		form  string
		spoil func(r *http.Request)
	}{
		{"headers", "tag=a&tag=b", func(r *http.Request) { r.Header["X-Tag"] = []string{"a"} }},
		{"forms", "tag=a", func(*http.Request) {}},
		{"cookie", "tag=a&tag=b", func(r *http.Request) { r.Header.Set(HeaderCookie, "session=xyz") }},
		{"basic_auth", "tag=a&tag=b", func(r *http.Request) { r.SetBasicAuth("jeff", "S3cret") }},
		{"host", "tag=a&tag=b", func(r *http.Request) { r.Host = "other.local" }},
		{"client_ip", "tag=a&tag=b", func(r *http.Request) { r.Header.Set("X-Remote-Addr", "10.0.0.2:1234") }},
	}

	for _, m := range []*Server{built, loaded} {
		require.Equal(t, http.StatusOK, send(m, "tag=a&tag=b", func(*http.Request) {}), string(desc))

		for _, miss := range misses {
			require.Equal(t, StatusNoMatch, send(m, miss.form, miss.spoil), "%s: %s", miss.name, desc)
		}
	}
}

func TestMock_ADescriptionKeepsTheScenarioAndRepeat(t *testing.T) {
	// A mock described its scenario and repeat as a single argument, which the description dropped, as it merged maps
	// only: saved through the admin API, a mock of one request served every request, outside its scenario. Each mock
	// below misses its second request only through the key it tests.
	tests := []struct {
		name    string
		give    *Builder
		wantKey string
		want    any
	}{
		{
			name:    "repeat",
			give:    Getf("/x").Times(1),
			wantKey: "repeat",
			want:    float64(1),
		},
		{
			name:    "scenario",
			give:    Getf("/x").ScenarioIs("checkout").ScenarioStateIs("started").ScenarioStateWillBe("paid"),
			wantKey: "scenario",
			want:    map[string]any{"name": "checkout", "required_state": "started", "new_state": "paid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built := MustNew(Setup().Silent())
			built.MustStart()
			t.Cleanup(built.Close)

			desc, err := built.MustMock(tt.give.Reply(OK())).Mocks()[0].MarshalJSON()
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(desc, &doc))
			require.Equal(t, tt.want, doc[tt.wantKey], string(desc))

			loaded := MustNew(Setup().Silent())
			loaded.MustStart()
			t.Cleanup(loaded.Close)
			loaded.MustMock(FromBytes(desc, "json"))

			require.Equal(t, http.StatusOK, getTarget(t, loaded, "/x"))
			require.Equal(t, StatusNoMatch, getTarget(t, loaded, "/x"), string(desc))
		})
	}
}

func TestMock_ADescriptionKeepsTheScenarioKey(t *testing.T) {
	// Saved through the admin API, a keyed mock stays keyed: each key still moves an instance of its own.
	built := MustNew(Setup().Silent())
	t.Cleanup(built.Close)

	desc, err := built.MustMock(Getf("/x").StartScenario("checkout").ScenarioStateWillBe("paid").
		ScenarioKey(`{{ .Request.Header.Get "X-Test" }}`).Reply(OK())).Mocks()[0].MarshalJSON()
	require.NoError(t, err)

	loaded := MustNew(Setup().Silent())
	loaded.MustStart()
	t.Cleanup(loaded.Close)
	loaded.MustMock(FromBytes(desc, "json"))

	for _, step := range []struct {
		key  string
		want int
	}{
		{"t1", http.StatusOK},
		{"t1", StatusNoMatch},
		{"t2", http.StatusOK},
	} {
		res, _ := do(t, http.MethodGet, loaded.URL("/x"), http.Header{"X-Test": {step.key}}, nil)
		require.Equal(t, step.want, res.StatusCode, "%s: %s", step.key, desc)
	}
}

// getTarget sends a GET with target as the request target, as is, and returns the status: Go's http.Client doesn't
// send a URL's fragment.
func getTarget(t *testing.T, m *Server, target string) int {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, m.URL(), nil)
	require.NoError(t, err)
	req.URL.Opaque = target

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode
}

func TestMock_ADescriptionKeepsTheEvents(t *testing.T) {
	// The description of a mock dropped its on_complete events: an admin GET, then POST, of a mock lost its webhooks.
	// It keeps them now, with their parameters as a mock file writes them, and when they run, and loads back with the
	// same events.
	got := make(chan vlatte.Parameters, 3)
	capture := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, e vlatte.OnComplete) error {
		got <- e.Parameters()
		return nil
	})

	m := MustNew(Setup().Silent().OnCompleteListener("capture", capture))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/x").ID("x").
		OnCompleteEvent(vlatte.EventConfig{Name: "capture", Parameters: map[string]any{"k": "v"}}).
		OnCompleteEvent(vlatte.EventConfig{Name: "capture"}).
		OnCompleteEvent(vlatte.EventConfig{Name: "capture", Parameters: map[string]any{"late": true}, AfterResponse: true}).
		Reply(NoContent()))

	desc, ok := scope.Mock("x").Describe().Props.(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{
		map[string]any{"name": "capture", "parameters": map[string]any{"k": "v"}},
		map[string]any{"name": "capture"},
		map[string]any{"name": "capture", "parameters": map[string]any{"late": true}, "after_response": true},
	}, desc["on_complete"])

	doc, err := json.Marshal(desc)
	require.NoError(t, err)

	require.NoError(t, m.Clean(t.Context()))
	again := m.MustMock(FromBytes(doc, "json"))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)
	require.ElementsMatch(t,
		[]vlatte.Parameters{map[string]any{"k": "v"}, nil, map[string]any{"late": true}},
		[]vlatte.Parameters{<-got, <-got, <-got})

	loaded, ok := again.Mock("x").Describe().Props.(map[string]any)
	require.True(t, ok)
	require.Equal(t, desc["on_complete"], loaded["on_complete"])
}

func TestMock_ADescriptionLeavesOutTheCallbacks(t *testing.T) {
	// A callback is a function: a mock file can't hold it. The description keeps the events a mock file names, and
	// loads back as a mock with those, and with no callback; it doesn't fail on the one it can't write.
	got := make(chan string, 4)
	capture := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
		got <- "event"
		return nil
	})

	m := MustNew(Setup().Silent().OnCompleteListener("capture", capture))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(
		Getf("/both").ID("both").
			Callback(func(context.Context, vlatte.OnComplete) error {
				got <- "callback"
				return nil
			}).
			OnCompleteEvent(vlatte.EventConfig{Name: "capture"}).
			Reply(NoContent()),
		Getf("/callback").ID("callback").
			Callback(func(context.Context, vlatte.OnComplete) error { return nil }).
			Reply(NoContent()),
	)

	both, ok := scope.Mock("both").Describe().Props.(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{map[string]any{"name": "capture"}}, both["on_complete"])

	only, ok := scope.Mock("callback").Describe().Props.(map[string]any)
	require.True(t, ok)
	require.NotContains(t, only, "on_complete", "a mock with only callbacks describes no events")

	doc, err := json.Marshal(both)
	require.NoError(t, err)

	require.NoError(t, m.Clean(t.Context()))
	m.MustMock(FromBytes(doc, "json"))

	status, _ := get(t, m, "/both")
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, "event", <-got)

	select {
	case extra := <-got:
		t.Fatalf("the loaded mock ran a %s", extra)
	default:
	}
}

func TestMock_ADescriptionOpensTheSameBodyFiles(t *testing.T) {
	// A relative body_file opens from the mock file's directory. Its description must name the file so that it opens
	// from the working directory, where a description posted back, or loaded with FromBytes, opens it (#38): it named
	// the file as written, so the mock loaded back failed, or sent another file.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("mocks", 0o700))

	files := map[string]string{
		"mocks/hello.yaml": "id: hello\nrequest: {method: GET, path: /hello}\nresponse: {status: 200, body_file: hello.txt}\n",
		"mocks/multi.yaml": "id: multi\nrequest: {method: GET, path: /multi}\nresponse:\n  status: 200\n" +
			"  multipart: {type: mixed, boundary: b, parts: [{content_type: text/plain, body_file: part.txt}]}\n",
		"mocks/upload.yaml": "id: upload\nrequest: {method: POST, path: /upload, form_file: {doc: {body_file: doc.txt}}}\n" +
			"response: {status: 201}\n",
		"mocks/hello.txt": "hello from a file",
		"mocks/part.txt":  "a part from a file",
		"mocks/doc.txt":   "an upload",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
	}

	get := func(path string) func(*testing.T, *Server) (int, string) {
		return func(t *testing.T, m *Server) (int, string) {
			res, b := fetch(t, m.URL(path))
			return res.StatusCode, string(b)
		}
	}

	upload := func(t *testing.T, m *Server) (int, string) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, err := w.CreateFormFile("doc", "doc.txt")
		require.NoError(t, err)
		_, err = part.Write([]byte("an upload"))
		require.NoError(t, err)
		require.NoError(t, w.Close())

		res, b := post(t, m.URL("/upload"), w.FormDataContentType(), body.Bytes())

		return res.StatusCode, string(b)
	}

	tests := []struct {
		file       string
		send       func(*testing.T, *Server) (int, string)
		wantStatus int
		wantBody   string
	}{
		{file: "mocks/hello.yaml", send: get("/hello"), wantStatus: http.StatusOK, wantBody: "hello from a file"},
		{file: "mocks/multi.yaml", send: get("/multi"), wantStatus: http.StatusOK, wantBody: "a part from a file"},
		{file: "mocks/upload.yaml", send: upload, wantStatus: http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			scope := m.MustMock(FromFile(tt.file))
			status, body := tt.send(t, m)
			require.Equal(t, tt.wantStatus, status, "from the file")
			require.Contains(t, body, tt.wantBody, "from the file")

			desc, err := scope.Mocks()[0].MarshalJSON()
			require.NoError(t, err)

			require.NoError(t, m.Unmock(t.Context(), scope))
			m.MustMock(FromBytes(desc, "json"))

			status, body = tt.send(t, m)
			require.Equal(t, tt.wantStatus, status, "loaded back: %s", desc)
			require.Contains(t, body, tt.wantBody, "loaded back: %s", desc)
		})
	}
}

func TestMock_ADisabledMockDescribesAsDisabled(t *testing.T) {
	// A disabled mock, read back and loaded again, as the admin API's save does, stays disabled: it came back enabled,
	// and matched requests it had been turned off for.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/off").Reply(OK()))
	scope.Disable()

	desc, err := scope.Mocks()[0].MarshalJSON()
	require.NoError(t, err)
	require.NoError(t, m.Unmock(t.Context(), scope))

	loaded := m.MustMock(FromBytes(desc, "json"))
	require.False(t, loaded.Mocks()[0].Enabled(), string(desc))
	require.Equal(t, StatusNoMatch, getTarget(t, m, "/off"))

	loaded.Enable()
	desc, err = loaded.Mocks()[0].MarshalJSON()
	require.NoError(t, err)
	require.NotContains(t, string(desc), "enabled", "enabled is the default")
}

func TestMock_FaultsDescribeAsTheirSpecs(t *testing.T) {
	// A mock's faults describe as a mock file lists them, each at its level, so the description loads back with them.
	// A step of Go code, which a mock file can't hold, is left out, as a callback is.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	upper := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		return next.Write(bytes.ToUpper(b))
	})

	scope := m.MustMock(Getf("/f").
		Step(fault.SlowClose(2*time.Second), upper, fault.LimitData(3)).
		WireStep(fault.ConnectionResetByPeer()).
		UpstreamStep(fault.Bandwidth(100)).
		Reply(OK()))

	want := map[string]any{
		"steps": []any{
			map[string]any{"type": "slow_close", "delay": "2s"},
			map[string]any{"type": "limit_data", "bytes": 3.0},
		},
		"wire_steps":     []any{map[string]any{"type": "connection_reset_by_peer"}},
		"upstream_steps": []any{map[string]any{"type": "bandwidth", "rate": 100.0}},
	}

	desc, err := scope.Mocks()[0].MarshalJSON()
	require.NoError(t, err)
	require.NoError(t, m.Unmock(t.Context(), scope))

	loaded := m.MustMock(FromBytes(desc, "json"))
	again, err := loaded.Mocks()[0].MarshalJSON()
	require.NoError(t, err)

	for _, d := range [][]byte{desc, again} {
		var got map[string]any
		require.NoError(t, json.Unmarshal(d, &got))

		for key, steps := range want {
			require.Equal(t, steps, got[key], key)
		}
	}
}

func TestMock_NoStepsDescribeAsNone(t *testing.T) {
	// A mock without faults, or with steps of Go code only, has no steps in its description: an empty list would be
	// a key the mock file never had.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	upper := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		return next.Write(bytes.ToUpper(b))
	})

	for _, b := range []*Builder{Getf("/a"), Getf("/b").Step(upper).WireStep(upper)} {
		desc, err := m.MustMock(b.Reply(OK())).Mocks()[0].MarshalJSON()
		require.NoError(t, err)
		require.NotContains(t, string(desc), "steps")
	}
}

// countingTarget starts a target that counts the connections it has open.
func countingTarget(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var open atomic.Int64

	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	target.Start()
	t.Cleanup(target.Close)

	return target, &open
}

// requireNoneOpen waits for the target to see every connection closed.
func requireNoneOpen(t *testing.T, open *atomic.Int64) {
	t.Helper()

	require.Eventually(t, func() bool { return open.Load() == 0 }, 2*time.Second, 10*time.Millisecond,
		"connections to the target left open: %d", open.Load())
}

// closingReplier answers 204 and counts its closes, as a replier holding a connection would.
type closingReplier struct {
	closes *atomic.Int64
}

func (r closingReplier) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusNoContent, Header: make(http.Header)}, nil
}

func (r closingReplier) Close() error {
	r.closes.Add(1)
	return nil
}

// closingReply makes a closingReplier for each mock, so each mock owns it.
type closingReply struct {
	closes *atomic.Int64
}

func (f closingReply) NewReplier(*Server) (Replier, error) {
	return closingReplier(f), nil
}

// brokenReply makes a replier that fails to close, as one whose connections are gone already.
type brokenReply struct {
	err error
}

func (f brokenReply) NewReplier(*Server) (Replier, error) {
	return brokenReplier(f), nil
}

type brokenReplier struct {
	err error
}

func (brokenReplier) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusNoContent, Header: make(http.Header)}, nil
}

func (r brokenReplier) Close() error {
	return r.err
}

// failingReply makes no replier.
type failingReply struct{}

func (failingReply) NewReplier(*Server) (Replier, error) {
	return nil, errors.New("no replier")
}

// countingTransport is a transport the caller shares, which a proxy must leave open. It counts the calls it carries,
// and the times it was asked to close its idle connections.
type countingTransport struct {
	http.RoundTripper

	calls  atomic.Int64
	closes atomic.Int64
}

// newCountingTransport returns a countingTransport that sends the calls it carries on.
func newCountingTransport(t *testing.T) *countingTransport {
	t.Helper()

	inner := &http.Transport{}
	t.Cleanup(inner.CloseIdleConnections)

	return &countingTransport{RoundTripper: inner}
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.RoundTripper.RoundTrip(r)
}

func (c *countingTransport) CloseIdleConnections() {
	c.closes.Add(1)
}

func TestProxyReply_ReleasesItsConnectionsWhenItLeaves(t *testing.T) {
	// #26: each build of a proxy mock makes a client of its own, which kept its idle connections to the target until
	// the server closed. A server that reloads mock files gained one connection on every reload.
	leave := []struct {
		name  string
		leave func(t *testing.T, m *Server, scope *Scope)
	}{
		{
			name: "remock",
			leave: func(t *testing.T, m *Server, scope *Scope) {
				_, err := m.Remock(t.Context(), scope, Getf("/other").Reply(OK()))
				require.NoError(t, err)
			},
		},
		{
			name: "unmock",
			leave: func(t *testing.T, m *Server, scope *Scope) {
				require.NoError(t, m.Unmock(t.Context(), scope))
			},
		},
		{
			name: "clean",
			leave: func(t *testing.T, m *Server, _ *Scope) {
				require.NoError(t, m.Clean(t.Context()))
			},
		},
		{
			name: "server close",
			leave: func(_ *testing.T, m *Server, _ *Scope) {
				m.Close()
			},
		},
	}

	for _, tc := range leave {
		t.Run(tc.name, func(t *testing.T) {
			target, open := countingTarget(t)

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			scope := m.MustMock(Getf("/p").Reply(From(target.URL)))

			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, int64(1), open.Load(), "the proxy keeps its connection to the target")

			tc.leave(t, m, scope)

			requireNoneOpen(t, open)
		})
	}

	t.Run("20 remocks", func(t *testing.T) {
		// The case of the issue: 20 requests, each followed by a Remock of the same mock, left 20 connections open.
		target, open := countingTarget(t)

		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		scope := m.MustMock(Getf("/p").Reply(From(target.URL)))
		for range 20 {
			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)

			var err error
			scope, err = m.Remock(t.Context(), scope, Getf("/p").Reply(From(target.URL)))
			require.NoError(t, err)
		}

		requireNoneOpen(t, open)
	})
}

func TestProxyReply_LeavesTheCallersTransportOpen(t *testing.T) {
	// A transport of the caller's can be one it shares, such as http.DefaultTransport: closing its idle connections
	// when a mock leaves would cut them for everyone else using it.
	tests := []struct {
		name  string
		setup func(shared http.RoundTripper) *ConfigBuilder
		reply func(target string, shared http.RoundTripper) *ProxyReply
	}{
		{
			name:  "the server's",
			setup: func(shared http.RoundTripper) *ConfigBuilder { return Setup().Transport(shared) },
			reply: func(target string, _ http.RoundTripper) *ProxyReply { return From(target) },
		},
		{
			name:  "the reply's",
			setup: func(http.RoundTripper) *ConfigBuilder { return Setup() },
			reply: func(target string, shared http.RoundTripper) *ProxyReply { return From(target).Transport(shared) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := countingTarget(t)
			shared := newCountingTransport(t)

			m := MustNew(tt.setup(shared).Silent())
			m.MustStart()

			scope := m.MustMock(Getf("/p").Reply(tt.reply(target.URL, shared)))

			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)

			require.NoError(t, m.Unmock(t.Context(), scope))
			m.Close()

			require.Zero(t, shared.closes.Load())
		})
	}
}

func TestShared_IsNotClosedWithItsMocks(t *testing.T) {
	// A shared replier serves every mock built with it: one of them leaving must not close it for the others.
	var closes atomic.Int64

	r := closingReplier{closes: &closes}

	m := MustNew(Setup().Silent())
	m.MustStart()

	a := m.MustMock(Getf("/a").Reply(Shared(r)))
	m.MustMock(Getf("/b").Reply(Shared(r)))

	require.NoError(t, m.Unmock(t.Context(), a))

	status, _ := get(t, m, "/b")
	require.Equal(t, http.StatusNoContent, status)

	m.Close()

	require.Zero(t, closes.Load(), "the caller owns a shared replier")
}

func TestCompositeReplies_CloseTheRepliesTheyOwn(t *testing.T) {
	// #26: sequences and random replies make a replier for each reply they hold, and so own it, but not a shared one.
	tests := []struct {
		name  string
		reply func(owned, shared ReplierFactory) ReplierFactory
		want  int64
	}{
		{
			name:  "sequence",
			reply: func(owned, shared ReplierFactory) ReplierFactory { return Seq(owned, shared).OnSequenceEnded(owned) },
			want:  2, // the first reply and after_ended
		},
		{
			name:  "random",
			reply: func(owned, shared ReplierFactory) ReplierFactory { return Rand(owned, shared) },
			want:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ownedCloses, sharedCloses atomic.Int64

			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			scope := m.MustMock(Getf("/x").Reply(tc.reply(closingReply{closes: &ownedCloses}, Shared(closingReplier{closes: &sharedCloses}))))
			require.NoError(t, m.Unmock(t.Context(), scope))

			require.Equal(t, tc.want, ownedCloses.Load())
			require.Zero(t, sharedCloses.Load())
		})
	}

	builds := []struct {
		name  string
		reply func(owned ReplierFactory) ReplierFactory
	}{
		{
			name:  "sequence with a failing reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned, failingReply{}) },
		},
		{
			name:  "sequence with a failing after_ended",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned).OnSequenceEnded(failingReply{}) },
		},
		{
			name:  "sequence with a nil reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned, nil) },
		},
		{
			name:  "random with a failing reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Rand(owned, failingReply{}) },
		},
		{
			name:  "random with a nil reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Rand(owned, nil) },
		},
	}

	for _, tc := range builds {
		t.Run(tc.name, func(t *testing.T) {
			// The replies built before the failing one are dropped with the build: nothing else would close them.
			var closes atomic.Int64

			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			_, err := m.Mock(Getf("/x").Reply(tc.reply(closingReply{closes: &closes})))
			require.Error(t, err)
			require.Equal(t, int64(1), closes.Load())
		})
	}
}

func TestServer_CloseLogsTheRepliesThatFailToClose(t *testing.T) {
	// A mock whose replies fail to close is done all the same: Close logs why, for each of them.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	m.MustMock(Getf("/x").ID("x").Reply(Seq(brokenReply{errors.New("first gone")}, brokenReply{errors.New("second gone")})))

	m.Close()
	require.Contains(t, logs.String(), "server: Close: closing mock x")
	require.Contains(t, logs.String(), "first gone")
	require.Contains(t, logs.String(), "second gone")
}

func TestMock_ClosesItsReplyOnce(t *testing.T) {
	// Server.Close releases what the stored mocks hold, and keeps them stored for assertions. A mock that leaves
	// later doesn't close its reply a second time.
	var closes atomic.Int64

	m := MustNew(Setup().Silent())
	m.MustStart()
	m.MustMock(Getf("/x").Reply(closingReply{closes: &closes}))

	m.Close()
	require.Equal(t, int64(1), closes.Load())
	require.Len(t, m.ListPending(), 1, "the mock is still there for assertions")

	require.NoError(t, m.Clean(t.Context()))
	require.Equal(t, int64(1), closes.Load(), "closed once")
}

func TestServer_AClosedServerDoesntStartAgain(t *testing.T) {
	// Close released what the mocks hold for good: a closed server doesn't start again, even one that never started.
	for name, start := range map[string]func(*Server) error{
		"Start":    (*Server).Start,
		"StartTLS": (*Server).StartTLS,
	} {
		t.Run(name+" after a start", func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			m.Close()

			require.ErrorIs(t, start(m), http.ErrServerClosed)
			require.Empty(t, m.URL(), "nothing listens")
		})

		t.Run(name+" before any start", func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.Close()

			require.ErrorIs(t, start(m), http.ErrServerClosed)
			require.Empty(t, m.URL(), "nothing listens")
		})
	}
}

func TestServer_CloseReleasesTheForwardProxyConnections(t *testing.T) {
	// #26: the transport of the forward proxy was never closed: its connections to the targets outlived the server.
	target, open := countingTarget(t)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL + "/x")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	m.Close()

	requireNoneOpen(t, open)
}

func TestServer_CloseLeavesTheCallersProxyTransportOpen(t *testing.T) {
	// A ProxyConfig.Transport is the caller's, as Config.Transport is.
	target, _ := countingTarget(t)

	inner := &http.Transport{}
	t.Cleanup(inner.CloseIdleConnections)

	shared := &countingTransport{RoundTripper: inner}

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Transport: shared, Timeout: time.Second}))
	m.MustStart()

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL + "/x")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	m.Close()

	require.Zero(t, shared.closes.Load())
}
