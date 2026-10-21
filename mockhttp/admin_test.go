package mockhttp

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// adminServer starts a server with its admin API, and the given setup.
func adminServer(t *testing.T, setup *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(setup.Silent().Admin(""))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// streamEvents follows the admin API's events of m while send sends n requests of n types of event, and returns the
// events by type. A request's event can come after the next request's answer: they are told apart by type, not order.
// It waits 5 seconds at most, so a missing event fails the test rather than hangs it.
func streamEvents(t *testing.T, m *Server, n int, send func()) map[any]map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, m.AdminURL()+"/events", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	require.Equal(t, "text/event-stream", res.Header.Get(HeaderContentType))

	send()

	lines := bufio.NewScanner(res.Body)
	byType := make(map[any]map[string]any)

	for len(byType) < n && lines.Scan() {
		line, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}

		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &e))
		byType[e["type"]] = e
	}

	require.Len(t, byType, n, "events: %v", byType)

	return byType
}

// object returns v, which must be an object of a document.
func object(t *testing.T, v any) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	require.True(t, ok, "%v is no object", v)

	return m
}

func TestAdmin_AddedMocksAreServed(t *testing.T) {
	// A mock posted to the admin API, in JSON or YAML, is served, and listed with the origin of the admin API.
	m := adminServer(t, Setup())

	res, _ := do(t, http.MethodPost, m.AdminURL()+"/mocks", http.Header{HeaderContentType: {MIMEApplicationJSON}},
		[]byte(`{"id": "json", "request": {"method": "GET", "path": "/json"}, "response": {"status": 201}}`))
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, _ = do(t, http.MethodPost, m.AdminURL()+"/mocks", http.Header{HeaderContentType: {"application/yaml"}},
		[]byte("id: yaml\nrequest: {method: GET, path: /yaml}\nresponse: {status: 202}\n"))
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, _ = do(t, http.MethodGet, m.URL("/json"), nil, nil)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, _ = do(t, http.MethodGet, m.URL("/yaml"), nil, nil)
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	_, body := do(t, http.MethodGet, m.AdminURL()+"/mocks", nil, nil)
	require.JSONEq(t, `{"mocks": [
		{"id": "json", "priority": 0, "enabled": true, "hits": 1, "origin": "admin"},
		{"id": "yaml", "priority": 0, "enabled": true, "hits": 1, "origin": "admin"}
	]}`, string(body))
}

func TestAdmin_AMalformedMockTellsItsProblems(t *testing.T) {
	// A malformed mock is the client's mistake: a 400 that says what to fix, as the mock file loader would.
	m := adminServer(t, Setup())

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil,
		[]byte(`{"request": {"method": "GET", "path": "/x"}, "response": {"stauts": 200}}`))

	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var doc struct {
		Code     string           `json:"code"`
		Problems []map[string]any `json:"problems"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "invalid_body", doc.Code)
	require.Len(t, doc.Problems, 1)
	require.Equal(t, "unknown_key", doc.Problems[0]["code"])
	require.Equal(t, "response.stauts", doc.Problems[0]["key"])
	require.Equal(t, "admin request", doc.Problems[0]["origin"])
}

func TestAdmin_AMockReadBackPostsBack(t *testing.T) {
	// A mock read from the admin API and posted back, as a client edits one, answers the same, its relative body
	// file included (#38): it opened from the working directory, not the mock file's.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("mocks", 0o700))
	require.NoError(t, os.WriteFile("mocks/hello.txt", []byte("hello from a file"), 0o600))
	require.NoError(t, os.WriteFile("mocks/hello.yaml",
		[]byte("id: hello\nrequest: {method: GET, path: /hello}\nresponse: {status: 200, body_file: hello.txt}\n"), 0o600))

	m := adminServer(t, Setup())
	m.MustMock(FromFile("mocks/hello.yaml"))

	res, desc := do(t, http.MethodGet, m.AdminURL()+"/mocks/hello", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(desc))

	again := strings.NewReplacer(`"hello"`, `"again"`, `"/hello"`, `"/again"`).Replace(string(desc))
	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(again))
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))

	res, body = do(t, http.MethodGet, m.URL("/again"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello from a file", string(body))
}

func TestAdmin_ProxyMocksReadBackAsTheyWere(t *testing.T) {
	// A proxy mock, with every option of its proxy, and a proxy_all mock, which has no request, read from the admin API
	// and put back, describe the same, and still proxy as they did.
	headers := make(chan http.Header, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		_, _ = w.Write([]byte("from the target " + r.URL.Path))
	}))
	t.Cleanup(target.Close)

	m := adminServer(t, Setup())

	for _, doc := range []string{
		`{"id": "proxied", "request": {"method": "GET", "path": "/api/*"}, "proxy": {"target": "` + target.URL + `",
			"request_header": {"X-Added": "yes"}, "header": {"X-Answer": "set"}, "remove_request_headers": ["X-Gone"],
			"trim_prefix": "/api", "timeout": "5s", "x_forwarded": true, "preserve_host": true}}`,
		`{"id": "rest", "proxy_all": {"target": "` + target.URL + `"}}`,
	} {
		res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(doc))
		require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	}

	for _, id := range []string{"proxied", "rest"} {
		_, desc := do(t, http.MethodGet, m.AdminURL()+"/mocks/"+id, nil, nil)

		res, body := do(t, http.MethodPut, m.AdminURL()+"/mocks/"+id, nil, desc)
		require.Equal(t, http.StatusOK, res.StatusCode, string(body))

		_, again := do(t, http.MethodGet, m.AdminURL()+"/mocks/"+id, nil, nil)
		require.JSONEq(t, string(desc), string(again), id)
	}

	res, body := do(t, http.MethodGet, m.URL("/api/users"), http.Header{"X-Gone": {"1"}}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "from the target /users", string(body))
	require.Equal(t, "set", res.Header.Get("X-Answer"))

	got := <-headers
	require.Equal(t, "yes", got.Get("X-Added"))
	require.Empty(t, got.Get("X-Gone"))
	require.NotEmpty(t, got.Get("X-Forwarded-For"))

	_, body = do(t, http.MethodGet, m.URL("/other"), nil, nil)
	require.Equal(t, "from the target /other", string(body), "proxy_all answers what no mock matches")
}

func TestAdmin_AReloadReplacesAdminMocksWithTheFilesOnes(t *testing.T) {
	// A mock file whose mock has the id of a mock added through the admin API takes its place on reload, as one
	// written by a save does: the reload doesn't fail over the id.
	dir := t.TempDir()
	m := adminServer(t, Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))

	res, _ := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil,
		[]byte(`{"id": "x", "request": {"method": "GET", "path": "/x"}, "response": {"status": 200, "body": "from the admin API"}}`))
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.yaml"),
		[]byte("id: x\nrequest: {method: GET, path: /x}\nresponse: {status: 200, body: from a file}\n"), 0o600))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/reload", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	_, body = do(t, http.MethodGet, m.URL("/x"), nil, nil)
	require.Equal(t, "from a file", string(body))
}

func TestAdmin_AReloadThatFailsTellsWhy(t *testing.T) {
	// A mock file with problems leaves the server as it was, and the admin API says which file and key.
	dir := t.TempDir()
	m := adminServer(t, Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("request: {method: GET, path: /bad}\nresponse: {status: nope}\n"), 0o600))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/reload", nil, nil)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)

	var doc struct {
		Code     string           `json:"code"`
		Problems []map[string]any `json:"problems"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "load_failed", doc.Code)
	require.Len(t, doc.Problems, 1)
	require.Equal(t, "response.status", doc.Problems[0]["key"])
	require.Contains(t, doc.Problems[0]["origin"], "bad.yaml")
}

func TestAdmin_RecordsAService(t *testing.T) {
	// A recording started through the admin API takes the requests sent to the mock server to its target, and once
	// stopped, the server answers them from the mock files it wrote, the target gone.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderContentType, MIMETextPlain)
		_, _ = w.Write([]byte("hello from " + r.URL.Path))
	}))

	dir := t.TempDir()
	m := adminServer(t, Setup())

	settings := `{"target": "` + target.URL + `", "dir": "` + filepath.ToSlash(dir) + `", "format": "yaml"}`
	res, body := do(t, http.MethodPost, m.AdminURL()+"/recording", nil, []byte(settings))
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))

	_, body = do(t, http.MethodGet, m.URL("/greeting"), nil, nil)
	require.Equal(t, "hello from /greeting", string(body))

	res, body = do(t, http.MethodDelete, m.AdminURL()+"/recording", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	var result struct {
		Status     map[string]any `json:"status"`
		Recordings []struct {
			File string `json:"file"`
		} `json:"recordings"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	require.Equal(t, false, result.Status["active"])
	require.Len(t, result.Recordings, 1)
	require.True(t, strings.HasSuffix(result.Recordings[0].File, ".mock.yaml"), result.Recordings[0].File)
	require.FileExists(t, result.Recordings[0].File)

	target.Close()

	res, body = do(t, http.MethodGet, m.URL("/greeting"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello from /greeting", string(body), "served from the recording")
}

func TestAdmin_RecordingSettingsWithProblems(t *testing.T) {
	// The settings of a recording are those of the record section of the server's settings, checked the same way.
	m := adminServer(t, Setup())

	res, body := do(t, http.MethodPost, m.AdminURL()+"/recording", http.Header{HeaderContentType: {"application/yaml"}},
		[]byte("format: xml\ntarget: ftp://example.com\nfrmat: json\n"))
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(body))

	var doc struct {
		Code     string           `json:"code"`
		Problems []map[string]any `json:"problems"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "invalid_body", doc.Code)

	keys := make([]any, 0, len(doc.Problems))
	for _, p := range doc.Problems {
		keys = append(keys, p["key"])
	}
	require.ElementsMatch(t, []any{"format", "target", "frmat"}, keys)

	_, body = do(t, http.MethodGet, m.AdminURL()+"/recording", nil, nil)
	require.JSONEq(t, `{"active": false, "calls": 0, "mocks": 0, "dropped": 0, "failed": 0}`, string(body), "nothing started")
}

func TestAdmin_RecordingSettingsTheRecordingRefuses(t *testing.T) {
	// A setting the recording's own checks refuse, such as an empty method, is a problem of the body too: not a
	// server error.
	m := adminServer(t, Setup())

	res, body := do(t, http.MethodPost, m.AdminURL()+"/recording", nil, []byte(`{"methods": ["GET", " "]}`))
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(body))
	require.Contains(t, string(body), "recorder: method 1 is empty")
}

func TestAdmin_MovesScenarios(t *testing.T) {
	// A test that needs a scenario in some state sets it, instead of replaying the requests that lead there, and starts
	// it over after.
	m := adminServer(t, Setup())
	m.MustMock(
		Getf("/pay").StartScenario("Checkout").ScenarioStateWillBe("paid").Reply(OK()),
		Getf("/pay").ScenarioIs("Checkout").ScenarioStateIs("paid").Reply(Status(http.StatusConflict)),
	)

	res, _ := do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = do(t, http.MethodDelete, m.AdminURL()+"/scenarios", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, _ = do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "started over")

	res, _ = do(t, http.MethodDelete, m.AdminURL()+"/scenarios", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, body := do(t, http.MethodPut, m.AdminURL()+"/scenarios/Checkout", nil, []byte(`{"state": "paid"}`))
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	res, _ = do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusConflict, res.StatusCode, "the mock of the state set")

	_, body = do(t, http.MethodGet, m.AdminURL()+"/scenarios", nil, nil)
	require.JSONEq(t, `{"scenarios": [{"name": "Checkout", "state": "paid"}]}`, string(body))
}

func TestAdmin_SavedMocksOutliveTheServer(t *testing.T) {
	// Mocks added through the admin API, saved, are served from their files from then on, by this server and by the
	// next one: the default mock file patterns load where the admin API saves by default. A relative body file still
	// opens: the file names it from its own directory.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("bodies", 0o700))
	require.NoError(t, os.WriteFile("bodies/hello.txt", []byte("hello from a file"), 0o600))

	m := adminServer(t, Setup())

	for _, doc := range []string{
		`{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body_file": "bodies/hello.txt"}}`,
		`{"id": "inline", "request": {"method": "GET", "path": "/inline"}, "response": {"status": 201, "body": "inline"}}`,
	} {
		res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(doc))
		require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	}

	m.MustMock(Getf("/code").Reply(OK()))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.JSONEq(t, `{"files": ["testdata/_mocks/hello.mock.yaml", "testdata/_mocks/inline.mock.yaml"]}`, string(body))

	saved, err := os.ReadFile("testdata/_mocks/hello.mock.yaml")
	require.NoError(t, err)
	require.Contains(t, string(saved), "body_file: ../../bodies/hello.txt")

	_, body = do(t, http.MethodGet, m.AdminURL()+"/mocks", nil, nil)
	require.Contains(t, string(body), `"origin": "testdata/_mocks/hello.mock.yaml"`, "served from its file")
	require.NotContains(t, string(body), `"origin": "admin"`)

	res, _ = do(t, http.MethodPost, m.AdminURL()+"/mocks/reload", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "the files reload without a conflict")

	next := MustNew(Setup().Silent())
	next.MustStart()
	t.Cleanup(next.Close)

	for _, s := range []*Server{m, next} {
		res, body = do(t, http.MethodGet, s.URL("/hello"), nil, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "hello from a file", string(body))

		res, _ = do(t, http.MethodGet, s.URL("/inline"), nil, nil)
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}

	res, _ = do(t, http.MethodGet, next.URL("/code"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode, "mocks added in code aren't saved")
}

func TestAdmin_SaveMovesOnlyTheBodyFilesTheServerOpens(t *testing.T) {
	// A saved mock file names its body files from its own directory. A key named body_file elsewhere, such as in a
	// template's data or another listener's parameters, isn't a file the server opens: it is saved as it was given.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("bodies", 0o700))
	require.NoError(t, os.WriteFile("bodies/hello.txt", []byte("hello from a file"), 0o600))

	nop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })
	m := adminServer(t, Setup().OnCompleteListener("audit", nop))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(`{
		"id": "hello",
		"request": {"method": "GET", "path": "/hello"},
		"response": {"status": 200, "body_file": "bodies/hello.txt", "template": {"data": {"body_file": "not-a-file"}}},
		"on_complete": [{"name": "audit", "parameters": {"body_file": "audit.log"}}]
	}`))
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))

	res, body = do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	data, err := os.ReadFile("testdata/_mocks/hello.mock.yaml")
	require.NoError(t, err)

	doc, err := config.Parse(data, "yaml")
	require.NoError(t, err)

	saved := object(t, doc)
	response := object(t, saved["response"])
	require.Equal(t, "../../bodies/hello.txt", response["body_file"])
	require.Equal(t, "not-a-file", object(t, object(t, response["template"])["data"])["body_file"])

	events, _ := saved["on_complete"].([]any)
	require.Len(t, events, 1)
	require.Equal(t, "audit.log", object(t, object(t, events[0])["parameters"])["body_file"])

	_, body = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, "hello from a file", string(body), "served from its file")
}

func TestRelocate_MovesTheBodyFilesOfTheMockFileFormatOnly(t *testing.T) {
	// The mock file format opens the body files of replies and their multipart parts, of form file patterns and of
	// webhooks: those move. A body_file anywhere else isn't one of its files, and stays.
	const doc = `{
		"request": {"form_file": {"one": {"body_file": %[1]q}, "many": [{"body_file": %[1]q}]}},
		"response": {
			"body_file": %[1]q,
			"multipart": {"parts": [{"body_file": %[1]q}]},
			"template": {"data": {"body_file": "kept"}}
		},
		"response_sequence": {"responses": [{"body_file": %[1]q}], "after_ended": {"body_file": %[1]q}},
		"response_random": {"responses": [{"body_file": %[1]q}]},
		"on_complete": [
			{"name": "webhook", "parameters": {
				"body_file": %[1]q,
				"multipart": {"parts": [{"body_file": %[1]q}]},
				"multipart_template": {"parts": [{"body_file": %[1]q}]}
			}},
			{"name": "audit", "parameters": {"body_file": "kept"}}
		],
		"ext": {"body_file": "kept"}
	}`

	up := filepath.Join("..", "..")

	var got, want any
	require.NoError(t, json.Unmarshal(fmt.Appendf(nil, doc, "body.txt"), &got))
	require.NoError(t, json.Unmarshal(fmt.Appendf(nil, doc, filepath.Join(up, "body.txt")), &want))

	relocate(got, up)
	require.Equal(t, want, got)

	relocate(got, "")
	require.Equal(t, want, got, "a mock file in the working directory names its files as the working directory does")
}

func TestAdmin_SaveWritesNothingWhenAMockCantBeSaved(t *testing.T) {
	// A mock whose description doesn't load back as the same mock, such as one with a trailer (#42), would write a
	// broken file, which every later start would fail on: nothing is written, the others neither.
	root := t.TempDir()
	m := adminServer(t, Setup().RootDir(root))

	for _, doc := range []string{
		`{"id": "fine", "request": {"method": "GET", "path": "/fine"}, "response": {"status": 200}}`,
		`{"id": "trailer", "request": {"method": "GET", "path": "/t"}, "response": {"status": 200, "trailer": {"X-T": "v"}}}`,
	} {
		res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(doc))
		require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	}

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, []byte(`{"dir": "saved"}`))
	require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))
	require.Contains(t, string(body), `"code": "describe_failed"`)
	require.Contains(t, string(body), `mock \"trailer\"`)

	_, err := os.Stat(filepath.Join(root, "saved"))
	require.ErrorIs(t, err, os.ErrNotExist, "nothing was written")
}

func TestAdmin_SaveThatFailsToLoadPutsTheFilesBack(t *testing.T) {
	// A saved file whose mock has the id of another mock file's fails to load: left on disk, it would fail every
	// later start. The save puts the files back as they were, those it overwrote too, and the server serves what it
	// served.
	root := t.TempDir()
	mocks := filepath.Join(root, "testdata", "_mocks")
	require.NoError(t, os.MkdirAll(mocks, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(mocks, "a.mock.yaml"),
		[]byte("id: x\nrequest: {method: GET, path: /x}\nresponse: {status: 200, body: from a.mock.yaml}\n"), 0o600))

	y := []byte("id: y\nrequest: {method: GET, path: /y}\nresponse: {status: 200, body: from y.mock.yaml}\n")
	require.NoError(t, os.WriteFile(filepath.Join(mocks, "y.mock.yaml"), y, 0o600))

	m := adminServer(t, Setup().RootDir(root).MockFilePatterns("testdata/_mocks/*mock.yaml"))

	for _, id := range []string{"x", "y"} {
		res, body := do(t, http.MethodPut, m.AdminURL()+"/mocks/"+id, nil,
			[]byte(`{"id": "`+id+`", "request": {"method": "GET", "path": "/`+id+`"}, "response": {"status": 200, "body": "from the admin API"}}`))
		require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	}

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, nil)
	require.Equal(t, http.StatusConflict, res.StatusCode, string(body))

	entries, err := os.ReadDir(mocks)
	require.NoError(t, err)
	require.Len(t, entries, 2, "x.mock.yaml was removed")

	saved, err := os.ReadFile(filepath.Join(mocks, "y.mock.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(y), string(saved), "y.mock.yaml holds what it held")

	_, body = do(t, http.MethodGet, m.URL("/x"), nil, nil)
	require.Equal(t, "from the admin API", string(body))

	next := MustNew(Setup().Silent().RootDir(root).MockFilePatterns("testdata/_mocks/*mock.yaml"))
	require.NoError(t, next.Start(), "a later start loads the files")
	next.Close()
}

func TestAdmin_SaveWithNothingToSave(t *testing.T) {
	// Only the mocks the admin API added are saved: with none, no file or directory is written.
	root := t.TempDir()
	m := adminServer(t, Setup().RootDir(root))
	m.MustMock(Getf("/code").Reply(OK()))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.JSONEq(t, `{"files": []}`, string(body))

	_, err := os.Stat(filepath.Join(root, "testdata"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAdmin_SaveThatCantWrite(t *testing.T) {
	// A save that can't write its files fails, and leaves none of them: the mocks stay the admin API's.
	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "a directory that is a file",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.WriteFile(filepath.Join(root, "saved"), nil, 0o600))
			},
		},
		{
			name: "a file that is a directory",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "saved", "b.mock.yaml"), 0o700))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)

			m := adminServer(t, Setup().RootDir(root))

			for _, id := range []string{"a", "b"} {
				res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil,
					[]byte(`{"id": "`+id+`", "request": {"method": "GET", "path": "/`+id+`"}, "response": {"status": 200}}`))
				require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
			}

			res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, []byte(`{"dir": "saved"}`))
			require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))
			require.Contains(t, string(body), `"code": "load_failed"`)

			require.NoFileExists(t, filepath.Join(root, "saved", "a.mock.yaml"))

			if entries, err := os.ReadDir(filepath.Join(root, "saved")); err == nil {
				require.Len(t, entries, 1, "no temporary file stays")
			}

			_, body = do(t, http.MethodGet, m.AdminURL()+"/mocks", nil, nil)
			require.Equal(t, 2, strings.Count(string(body), `"origin": "admin"`), string(body))
		})
	}
}

func TestAdmin_ASavedDirectoryThatCantBeReadFailsTheReload(t *testing.T) {
	// The mocks saved load with the mock files from then on: a reload that can't read their directory fails, and says
	// which.
	root := t.TempDir()
	m := adminServer(t, Setup().RootDir(root))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil,
		[]byte(`{"id": "a", "request": {"method": "GET", "path": "/a"}, "response": {"status": 200}}`))
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))

	res, body = do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, []byte(`{"dir": "saved"}`))
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	saved := filepath.Join(root, "saved")
	require.NoError(t, os.RemoveAll(saved))
	require.NoError(t, os.WriteFile(saved, nil, 0o600))

	res, body = do(t, http.MethodPost, m.AdminURL()+"/mocks/reload", nil, nil)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))
	require.Contains(t, string(body), "loader: saved mocks "+saved)
}

func TestAdmin_RecordsWithTheDefaultSettings(t *testing.T) {
	// A recording started without settings records with the server's: its defaults, when it has none.
	m := adminServer(t, Setup().RootDir(t.TempDir()))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/recording", nil, nil)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	require.Contains(t, string(body), `"active": true`)

	res, body = do(t, http.MethodDelete, m.AdminURL()+"/recording", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
}

func TestAdmin_ResetsTheServer(t *testing.T) {
	// A reset between tests: the mocks the admin API added go, the mock files load again, and scenarios start over.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.yaml"),
		[]byte("id: file\nrequest: {method: GET, path: /file}\nresponse: {status: 200}\n"), 0o600))

	m := adminServer(t, Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))
	m.MustMock(
		Getf("/pay").StartScenario("checkout").ScenarioStateWillBe("paid").Reply(OK()),
		Getf("/pay").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(Status(http.StatusConflict)),
	)

	res, _ := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil,
		[]byte(`{"id": "added", "request": {"method": "GET", "path": "/added"}, "response": {"status": 200}}`))
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, _ = do(t, http.MethodDelete, m.AdminURL()+"/mocks/file", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, _ = do(t, http.MethodGet, m.URL("/pay"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body := do(t, http.MethodPost, m.AdminURL()+"/reset", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	for path, want := range map[string]int{"/added": StatusNoMatch, "/file": http.StatusOK, "/pay": http.StatusOK} {
		res, _ = do(t, http.MethodGet, m.URL(path), nil, nil)
		require.Equal(t, want, res.StatusCode, path)
	}
}

func TestAdmin_StreamsWhatTheServerServes(t *testing.T) {
	// A client follows the requests the mocks answer, and those no mock matched, as they come: to see why a test's
	// request missed, without a journal of requests.
	m := adminServer(t, Setup())
	m.MustMock(Getf("/hello").ID("hello").Reply(OK()))

	byType := streamEvents(t, m, 2, func() {
		do(t, http.MethodGet, m.URL("/hello"), nil, nil)
		do(t, http.MethodPost, m.URL("/nope"), nil, nil)
	})

	matched := byType["matched"]
	require.Equal(t, "hello", matched["mock_id"])
	require.Equal(t, float64(http.StatusOK), matched["status"])
	require.Equal(t, "GET", matched["method"])

	unmatched := byType["unmatched"]
	require.Equal(t, "POST", unmatched["method"])
	require.Contains(t, unmatched["target"], "/nope")
}

func TestAdmin_StreamsWhatTheProxyServes(t *testing.T) {
	// A request no mock matches goes to its host when the server is a proxy: a client follows it with the host's status,
	// and follows the error when the host can't be reached, as for the requests the mocks answer.
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(host.Close)

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	m := adminServer(t, Setup().Proxy())
	client := proxyClient(t, m)

	byType := streamEvents(t, m, 2, func() {
		for _, target := range []string{host.URL + "/up", down.URL + "/down"} {
			res, err := client.Get(target)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
		}
	})

	proxied := byType["proxied"]
	require.Equal(t, float64(http.StatusAccepted), proxied["status"])
	require.Contains(t, proxied["target"], "/up")

	failed := byType["error"]
	require.Contains(t, failed["target"], "/down")
	require.Contains(t, failed["message"], "proxy: calling the target "+down.URL+"/down")
}

func TestAdmin_StreamsTheLog(t *testing.T) {
	// The log lines the server writes reach the admin API's clients too, such as for a server in a container.
	m := MustNew(Setup().Admin("").LogLevel(vlatte.LogLevelInfo))
	m.MustStart()
	t.Cleanup(m.Close)

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.AdminURL()+"/logs", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	do(t, http.MethodGet, m.URL("/logged"), nil, nil)

	lines := bufio.NewScanner(res.Body)
	for lines.Scan() {
		if line, ok := strings.CutPrefix(lines.Text(), "data: "); ok && strings.Contains(line, "/logged") {
			var entry map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
			require.Equal(t, "info", entry["level"])

			return
		}
	}

	t.Fatal("no log line of the request")
}

func TestAdmin_NoLogStreamForALoggerItWasGiven(t *testing.T) {
	// A logger the server was given writes where it was told to: the admin API doesn't take its lines.
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	res, _ := do(t, http.MethodGet, m.AdminURL()+"/logs", nil, nil)
	require.Equal(t, http.StatusNotImplemented, res.StatusCode)
}

func TestAdmin_ResetPutsBackTheFilesMock(t *testing.T) {
	// A mock file's mock the admin API replaced comes back from its file on reset: the file's mock takes the place of
	// the admin API's in one step, and none of the admin API's stays.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.yaml"),
		[]byte("id: x\nrequest: {method: GET, path: /x}\nresponse: {status: 200, body: from a file}\n"), 0o600))

	m := adminServer(t, Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))

	res, body := do(t, http.MethodPut, m.AdminURL()+"/mocks/x", nil,
		[]byte(`{"id": "x", "request": {"method": "GET", "path": "/x"}, "response": {"status": 200, "body": "from the admin API"}}`))
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	res, body = do(t, http.MethodPost, m.AdminURL()+"/reset", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	_, body = do(t, http.MethodGet, m.URL("/x"), nil, nil)
	require.Equal(t, "from a file", string(body))

	_, body = do(t, http.MethodGet, m.AdminURL()+"/mocks", nil, nil)
	require.NotContains(t, string(body), `"origin": "admin"`)
}
