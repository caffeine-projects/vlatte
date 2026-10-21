package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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
	// It keeps them now, with their parameters as a mock file writes them, and loads back with the same events.
	got := make(chan vlatte.Parameters, 2)
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
		Reply(NoContent()))

	desc, ok := scope.Mock("x").Describe().Props.(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{
		map[string]any{"name": "capture", "parameters": map[string]any{"k": "v"}},
		map[string]any{"name": "capture"},
	}, desc["on_complete"])

	doc, err := json.Marshal(desc)
	require.NoError(t, err)

	require.NoError(t, m.Clean(t.Context()))
	m.MustMock(FromBytes(doc, "json"))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)
	require.ElementsMatch(t, []vlatte.Parameters{map[string]any{"k": "v"}, nil}, []vlatte.Parameters{<-got, <-got})
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
