package handler_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ vlatte.Mock                = (*fakeMock)(nil)
	_ descriptor.SelfDescriptive = (*fakeMock)(nil)
	_ io.Closer                  = (*fakeMock)(nil)
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
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

func (m *fakeMock) Describe() descriptor.Descriptor { return descriptor.Root(m.doc) }

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

	mock := &fakeMock{id: id, name: name, priority: int(priority), origin: handler.Origin, doc: m}
	mock.enabled.Store(true)
	b.built = append(b.built, mock)

	return mock, nil
}

// brokenStore is a memory store whose operations fail with the errors it has, and work otherwise. It counts the
// calls of Replace.
type brokenStore struct {
	*vlatte.MemoryStore[*fakeMock]

	storeErr, replaceErr, pickErr, pickAllErr, clearErr error

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

// serve starts an admin API of store, whose mocks requests build with a fakeBuilder, and returns its URL.
func serve(t *testing.T, store vlatte.Store[*fakeMock], options ...admin.Option) string {
	t.Helper()

	return serveWith(t, store, &fakeBuilder{}, options...)
}

// serveWith starts an admin API of store, whose mocks requests build with b, and returns its URL.
func serveWith(t *testing.T, store vlatte.Store[*fakeMock], b *fakeBuilder, options ...admin.Option) string {
	t.Helper()

	options = append([]admin.Option{admin.Mocks[*fakeMock](store, b)}, options...)

	s, err := admin.New(options...)
	require.NoError(t, err)
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	return s.URL()
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
