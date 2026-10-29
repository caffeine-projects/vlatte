package handler_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

var (
	_ vlatte.Mock            = (*fakeMock)(nil)
	_ vlatte.SelfDescriptive = (*fakeMock)(nil)
	_ io.Closer              = (*fakeMock)(nil)
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeMock is a mock of no protocol: the admin API knows mocks through vlatte.Mock and the interfaces it checks for.
type fakeMock struct {
	id       string
	name     string
	priority int
	origin   string
	doc      map[string]any
	enabled  atomic.Bool
	hits     atomic.Int64
	closed   atomic.Int64
}

func (m *fakeMock) ID() string            { return m.id }
func (m *fakeMock) Name() string          { return m.name }
func (m *fakeMock) Priority() int         { return m.priority }
func (m *fakeMock) Hits() int64           { return m.hits.Load() }
func (m *fakeMock) Called() bool          { return m.hits.Load() > 0 }
func (m *fakeMock) Enabled() bool         { return m.enabled.Load() }
func (m *fakeMock) Enable()               { m.enabled.Store(true) }
func (m *fakeMock) Disable()              { m.enabled.Store(false) }
func (m *fakeMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *fakeMock) String() string        { return m.id }
func (m *fakeMock) Origin() string        { return m.origin }

func (m *fakeMock) Describe() vlatte.Descriptor { return vlatte.DescribeRoot(m.doc) }

func (m *fakeMock) Close() error {
	m.closed.Add(1)
	return nil
}

// fakeBuilder builds fake mocks from documents in JSON or YAML: an object with an id, a name and a priority, all
// optional, and anything else, which the mock describes as given. A document without an id gets a generated one, as
// builders give. An unknown key "bad" is a problem. It keeps every mock it builds.
type fakeBuilder struct {
	mu    sync.Mutex
	built []*fakeMock
	seq   int
}

func (b *fakeBuilder) BuildFromBytes(content []byte, format string) (*fakeMock, error) {
	doc, err := config.Parse(content, format)
	if err != nil {
		return nil, &config.Error{Scope: "mock", Problems: []config.Problem{{
			Code: config.CodeParseError, Origin: "admin request", Message: err.Error(),
		}}}
	}

	m, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("fake mock: not an object")
	}

	if _, bad := m["bad"]; bad {
		return nil, &config.Error{Scope: "mock", Problems: []config.Problem{{
			Code: config.CodeUnknownKey, Origin: "admin request", Key: "bad", Message: `unknown key "bad"`,
		}}}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	id, _ := m["id"].(string)
	if id == "" {
		b.seq++
		id = "generated-" + strconv.Itoa(b.seq)
		m["id"] = id
	}

	name, _ := m["name"].(string)
	priority, _ := m["priority"].(int64)

	mock := &fakeMock{id: id, name: name, priority: int(priority), origin: adminapi.Origin, doc: m}
	mock.enabled.Store(true)
	b.built = append(b.built, mock)

	return mock, nil
}

// brokenStore is a memory store whose operations fail with the errors it has, and work otherwise. It counts the
// calls of Replace.
type brokenStore struct {
	*vlatte.MemoryStore[*fakeMock]

	storeErr, replaceErr, pickErr, pickAllErr, clearErr error
	healthErr                                           error

	replaces atomic.Int64
}

func (s *brokenStore) Store(ctx context.Context, mocks ...*fakeMock) error {
	if s.storeErr != nil {
		return s.storeErr
	}

	return s.MemoryStore.Store(ctx, mocks...)
}

func (s *brokenStore) Replace(ctx context.Context, old []*fakeMock, mocks ...*fakeMock) error {
	s.replaces.Add(1)

	if s.replaceErr != nil {
		return s.replaceErr
	}

	return s.MemoryStore.Replace(ctx, old, mocks...)
}

func (s *brokenStore) Pick(ctx context.Context, id string) (*fakeMock, error) {
	if s.pickErr != nil {
		return nil, s.pickErr
	}

	return s.MemoryStore.Pick(ctx, id)
}

func (s *brokenStore) PickAll(ctx context.Context) ([]*fakeMock, error) {
	if s.pickAllErr != nil {
		return nil, s.pickAllErr
	}

	return s.MemoryStore.PickAll(ctx)
}

func (s *brokenStore) Health(ctx context.Context) error {
	if s.healthErr != nil {
		return s.healthErr
	}

	return s.MemoryStore.Health(ctx)
}

func (s *brokenStore) Clear(ctx context.Context) error {
	if s.clearErr != nil {
		return s.clearErr
	}

	return s.MemoryStore.Clear(ctx)
}

// newFake returns an enabled fake mock with id, described as {"id": id}.
func newFake(id string) *fakeMock {
	m := &fakeMock{id: id, doc: map[string]any{"id": id}}
	m.enabled.Store(true)

	return m
}

// serve starts an admin API of store, whose mocks requests build with a fakeBuilder, with the capabilities of caps,
// if any, and returns its URL.
func serve(t *testing.T, store vlatte.Store[*fakeMock], caps ...adminapi.Capabilities) string {
	t.Helper()

	return serveWith(t, store, &fakeBuilder{}, caps...)
}

// serveWith starts an admin API of store, whose mocks requests build with b, with the capabilities of caps, if any,
// and returns its URL.
func serveWith(t *testing.T, store vlatte.Store[*fakeMock], b *fakeBuilder, caps ...adminapi.Capabilities) string {
	t.Helper()

	var c adminapi.Capabilities
	if len(caps) > 0 {
		c = caps[0]
	}

	return start(t, admin.Options{}, store, b, c).URL()
}

// start starts an admin API of opts, of store, whose mocks requests build with b, with the capabilities of caps.
func start(t *testing.T, opts admin.Options, store vlatte.Store[*fakeMock], b *fakeBuilder, caps adminapi.Capabilities) *admin.Server {
	t.Helper()

	caps.Mocks = adminapi.MocksOf[*fakeMock](store, b)

	s, err := admin.New(opts, caps)
	require.NoError(t, err)
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	return s
}

// connect starts an admin API like serve, which serves MCP, and connects an MCP client to it, as an agent does, over
// Streamable HTTP.
func connect(t *testing.T, store vlatte.Store[*fakeMock], caps ...adminapi.Capabilities) *mcp.ClientSession {
	t.Helper()

	return connectWith(t, store, &fakeBuilder{}, caps...)
}

// connectWith is like connect, with mocks that b builds.
func connectWith(t *testing.T, store vlatte.Store[*fakeMock], b *fakeBuilder, caps ...adminapi.Capabilities) *mcp.ClientSession {
	t.Helper()

	var c adminapi.Capabilities
	if len(caps) > 0 {
		c = caps[0]
	}

	s := start(t, admin.Options{MCP: true}, store, b, c)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: s.URL() + "/mcp"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// call calls the tool name with args, which must succeed, and returns the text of its result.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()

	text, failed := callTool(t, cs, name, args)
	require.False(t, failed, text)

	return text
}

// callFails calls the tool name with args, which must fail with the problem of status and code, as the OpenAPI
// document documents it, and returns it.
func callFails(t *testing.T, cs *mcp.ClientSession, name string, args any, status int, code string) map[string]any {
	t.Helper()

	text, failed := callTool(t, cs, name, args)
	require.True(t, failed, text)

	doc := conformsAs(t, text, "json", "Problem")
	require.Equal(t, code, doc["code"], text)
	require.Equal(t, int64(status), doc["status"])
	require.Equal(t, name, doc["instance"], "the instance is the tool")

	return doc
}

// callTool calls the tool name with args, and returns the text of its result, and whether it failed.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "the result is text")

	return text.Text, res.IsError
}

// newStore returns a store holding mocks.
func newStore(t *testing.T, mocks ...*fakeMock) vlatte.Store[*fakeMock] {
	t.Helper()

	store := vlatte.NewStore[*fakeMock]()
	require.NoError(t, store.Store(t.Context(), mocks...))

	return store
}

// request is a request to the admin API.
type request struct {
	method      string
	url         string
	contentType string
	accept      string
	body        string
	set         func(r *http.Request) // changes the request, such as to add credentials, when not nil
}

// send sends req and returns the response, with its body read.
func send(t *testing.T, req request) (*http.Response, string) {
	t.Helper()

	r, err := http.NewRequestWithContext(t.Context(), req.method, req.url, strings.NewReader(req.body))
	require.NoError(t, err)

	if req.contentType != "" {
		r.Header.Set("Content-Type", req.contentType)
	}

	if req.accept != "" {
		r.Header.Set("Accept", req.accept)
	}

	if req.set != nil {
		req.set(r)
	}

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	return res, readBody(t, res)
}

// readBody reads the body of res, and closes it.
func readBody(t *testing.T, res *http.Response) string {
	t.Helper()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return string(b)
}

// _spec compiles the schemas of the OpenAPI document, so answers are checked against what it documents.
var _spec = sync.OnceValues(func() (*jsonschema.Compiler, error) {
	doc, err := config.Parse(handler.OpenAPIDocument, "yaml")
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	spec, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)

	return c, c.AddResource("mem://admin/openapi.json", spec)
})

// conforms checks that body, in JSON or YAML as the response says, is what the OpenAPI document's schema name
// documents, and returns it as a document.
func conforms(t *testing.T, res *http.Response, body, schema string) map[string]any {
	t.Helper()

	format := "json"
	if strings.Contains(res.Header.Get("Content-Type"), "yaml") {
		format = "yaml"
	}

	return conformsAs(t, body, format, schema)
}

// conformsAs checks that body, a document in format, is what the OpenAPI document's schema name documents, and
// returns it as a document.
func conformsAs(t *testing.T, body, format, schema string) map[string]any {
	t.Helper()

	doc, err := config.Parse([]byte(body), format)
	require.NoError(t, err, body)

	data, err := json.Marshal(doc)
	require.NoError(t, err)

	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)

	c, err := _spec()
	require.NoError(t, err)

	sch, err := c.Compile("mem://admin/openapi.json#/components/schemas/" + schema)
	require.NoError(t, err)
	require.NoError(t, sch.Validate(instance), body)

	m, ok := doc.(map[string]any)
	require.True(t, ok, body)

	return m
}

// problem checks that the answer is a problem document of status and code, as the OpenAPI document documents it, and
// returns it.
func problem(t *testing.T, res *http.Response, body string, status int, code string) map[string]any {
	t.Helper()

	require.Equal(t, status, res.StatusCode, body)
	require.Contains(t, res.Header.Get("Content-Type"), "application/problem+", body)

	doc := conforms(t, res, body, "Problem")
	require.Equal(t, code, doc["code"], body)
	require.Equal(t, int64(status), doc["status"])
	require.Equal(t, http.StatusText(status), doc["title"])
	require.Equal(t, res.Request.URL.Path, doc["instance"])

	return doc
}

// ids returns the ids of the mocks of store.
func ids(t *testing.T, store vlatte.Store[*fakeMock]) []string {
	t.Helper()

	all, err := store.PickAll(t.Context())
	require.NoError(t, err)

	out := make([]string, 0, len(all))
	for _, m := range all {
		out = append(out, m.ID())
	}

	return out
}

// fakeRecorder records nothing: it keeps the settings it starts with, and answers what it is told to.
type fakeRecorder struct {
	doc      string
	format   string
	status   vlatte.RecordStatus
	startErr error
	result   *vlatte.RecordResult
	stopErr  error
}

func (r *fakeRecorder) StartRecording(doc []byte, format string) error {
	if r.startErr != nil {
		return r.startErr
	}

	r.doc, r.format = string(doc), format
	r.status.Active = true

	return nil
}

func (r *fakeRecorder) StopRecording(context.Context) (*vlatte.RecordResult, error) {
	return r.result, r.stopErr
}

func (r *fakeRecorder) RecordingStatus() vlatte.RecordStatus {
	return r.status
}

// journalOf returns a journal of entries, the protocol's own: a path, and its method.
func journalOf(t *testing.T, entries ...*journal.Entry) *journal.Journal {
	t.Helper()

	j, err := journal.New(journal.Options{Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

func entryOf(id, method, path string, outcome journal.Outcome) *journal.Entry {
	return &journal.Entry{
		ID:      id,
		Time:    time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		Kind:    "fake",
		Outcome: outcome,
		Method:  method,
		Path:    path,
		Target:  "fake://" + path,
		Status:  200,
		Header:  map[string][]string{"Accept": {"application/json"}},
		Body:    []byte(`{"id": 7}`),
	}
}

// requestIDs returns the ids of the requests of a RequestList.
func requestIDs(doc map[string]any) []string {
	list, _ := doc["requests"].([]any)

	ids := make([]string, 0, len(list))
	for _, r := range list {
		e, _ := r.(map[string]any)
		id, _ := e["id"].(string)
		ids = append(ids, id)
	}

	return ids
}

// fakeScenarios keeps scenarios in a real store, for the scenario matchers of mocks of uses. listErr fails listing
// them, and setErr moving one; unlisted lists none.
type fakeScenarios struct {
	store    *matchscenario.Store
	uses     []matchscenario.Use
	listErr  error
	setErr   error
	unlisted bool
}

var _ adminapi.Scenarios = (*fakeScenarios)(nil)

// newScenarios keeps scenarios for mocks, each a mock ID and the scenario, the state it requires and the one it moves
// to.
func newScenarios(mocks ...[4]string) *fakeScenarios {
	f := &fakeScenarios{store: matchscenario.New()}
	for _, m := range mocks {
		f.uses = append(f.uses, matchscenario.Use{MockID: m[0], Matcher: f.store.Matcher(m[1], m[2], m[3])})
	}

	return f
}

func (f *fakeScenarios) Scenarios(context.Context) ([]matchscenario.Scenario, error) {
	if f.listErr != nil || f.unlisted {
		return nil, f.listErr
	}

	return f.store.Model(slices.Values(f.uses)), nil
}

func (f *fakeScenarios) SetScenario(_ context.Context, name, state string, in ...matchscenario.Instance) error {
	if f.setErr != nil {
		return f.setErr
	}

	return f.store.Move(slices.Values(f.uses), name, state, in...)
}

func (f *fakeScenarios) ResetScenario(name string, in ...matchscenario.Instance) {
	f.store.ResetScenario(name, in...)
}

func (f *fakeScenarios) ResetScenarios() { f.store.ResetScenarios() }
