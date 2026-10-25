package mockhttp

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// mockFault is an invalid setting injected into a valid mock document, and the problem it must cause.
type mockFault struct {
	area   string // what it touches, so two faults never touch the same thing
	code   string
	key    string
	inject func(doc map[string]any)
}

// _mockFaults touch distinct settings of the base document of genFaulty, field by field.
var _mockFaults = []mockFault{
	{"unknown", config.CodeUnknownKey, "zzz", func(doc map[string]any) { doc["zzz"] = int64(1) }},
	{"status", config.CodeInvalidValue, "response.status", func(doc map[string]any) { sub(doc, "response")["status"] = "abc" }},
	{"typo", config.CodeUnknownKey, "response.stauts", func(doc map[string]any) { sub(doc, "response")["stauts"] = int64(201) }},
	{"encoding", config.CodeInvalidValue, "response.encoding", func(doc map[string]any) { sub(doc, "response")["encoding"] = "br" }},
	{"header", config.CodeInvalidValue, "response.header.X-Count", func(doc map[string]any) {
		sub(doc, "response")["header"] = map[string]any{"X-Count": int64(10)}
	}},
	{"method", config.CodeInvalidValue, "request.method", func(doc map[string]any) { sub(doc, "request")["method"] = int64(5) }},
	{"url_match", config.CodeInvalidValue, "request.url_match", func(doc map[string]any) {
		r := sub(doc, "request")
		delete(r, "path")
		r["url_match"] = "("
	}},
	{"path_params", config.CodeMissingValue, "request.path_params", func(doc map[string]any) {
		sub(doc, "request")["path_params"] = map[string]any{"id": "7"}
	}},
	{"query", config.CodeInvalidValue, "request.query.page", func(doc map[string]any) {
		sub(doc, "request")["query"] = map[string]any{"page": int64(1)}
	}},
	{"matcher", config.CodeInvalidValue, "request.header.X-Id", func(doc map[string]any) {
		sub(doc, "request")["header"] = map[string]any{"X-Id": []any{"gt"}}
	}},
	{"headers", config.CodeInvalidValue, "request.headers.X-Tag", func(doc map[string]any) {
		sub(doc, "request")["headers"] = map[string]any{"X-Tag": "a"}
	}},
	{"queries", config.CodeInvalidValue, "request.queries.page", func(doc map[string]any) {
		sub(doc, "request")["queries"] = map[string]any{"page": "2"}
	}},
	{"basic_auth", config.CodeMissingValue, "request.basic_auth.password", func(doc map[string]any) {
		sub(doc, "request")["basic_auth"] = map[string]any{"username": "u"}
	}},
	{"delay", config.CodeInvalidValue, "delay", func(doc map[string]any) { doc["delay"] = int64(-5) }},
	{"repeat", config.CodeInvalidValue, "repeat", func(doc map[string]any) { doc["repeat"] = int64(0) }},
	{"id", config.CodeInvalidValue, "id", func(doc map[string]any) { doc["id"] = "a/b" }},
	{"proxy", config.CodeConflict, "proxy", func(doc map[string]any) { doc["proxy"] = map[string]any{"target": "http://localhost"} }},
	{"on_complete", config.CodeInvalidValue, "on_complete", func(doc map[string]any) { doc["on_complete"] = map[string]any{"name": "x"} }},
	{"scenario", config.CodeMissingValue, "scenario.name", func(doc map[string]any) {
		doc["scenario"] = map[string]any{"required_state": "a"}
	}},
	{"ext", config.CodeInvalidValue, "ext", func(doc map[string]any) { doc["ext"] = []any{int64(1)} }},
	{"name", config.CodeInvalidValue, "name", func(doc map[string]any) { doc["name"] = int64(123) }},
	{"priority", config.CodeInvalidValue, "priority", func(doc map[string]any) { doc["priority"] = "5" }},
	{"enabled", config.CodeInvalidValue, "enabled", func(doc map[string]any) { doc["enabled"] = "yes" }},
	// The form_file faults share an area: one sets the whole form_file, which would hide the others.
	{"form_file", config.CodeInvalidValue, "request.form_file", func(doc map[string]any) { sub(doc, "request")["form_file"] = []any{} }},
	{"form_file", config.CodeUnknownKey, "request.form_file.avatar.filname", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"filname": "a.png"}}
	}},
	{"form_file", config.CodeConflict, "request.form_file.avatar.body_file", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"body": "x", "body_file": "a.png"}}
	}},
	{"form_file", config.CodeFileNotFound, "request.form_file.avatar.body_file", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"body_file": "testdata/no-such-file.png"}}
	}},
	{"form_file", config.CodeInvalidValue, "request.form_file.avatar[1].every", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": []any{make(map[string]any), map[string]any{"every": "x"}}}
	}},
	// The multipart faults share an area: each sets response.multipart and drops the body it would conflict with.
	{"multipart", config.CodeInvalidValue, "response.multipart.type", func(doc map[string]any) { withMultipart(doc)["type"] = "multipart/mixed" }},
	{"multipart", config.CodeMissingValue, "response.multipart.type", func(doc map[string]any) { delete(withMultipart(doc), "type") }},
	{"multipart", config.CodeInvalidValue, "response.multipart.boundary", func(doc map[string]any) { withMultipart(doc)["boundary"] = "a b " }},
	{"multipart", config.CodeNotAllowed, "response.multipart.params.boundary", func(doc map[string]any) {
		withMultipart(doc)["params"] = map[string]any{"boundary": "x"}
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts", func(doc map[string]any) { withMultipart(doc)["parts"] = []any{} }},
	// Subtypes ignore case, in the decoder and in the editor schema.
	{"multipart", config.CodeMissingValue, "response.multipart.parts[0].name", func(doc map[string]any) { withMultipart(doc)["type"] = "Form-Data" }},
	{"multipart", config.CodeUnknownKey, "response.multipart.parts[0].nmae", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"nmae": "x"}}
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts[0].delay", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"delay": int64(-5)}}
	}},
	{"multipart", config.CodeConflict, "response.multipart", func(doc map[string]any) {
		withMultipart(doc)
		sub(doc, "response")["body"] = "x"
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts[0].body", func(doc map[string]any) {
		multipart := withMultipart(doc)
		multipart["boundary"] = "b"
		multipart["parts"] = []any{map[string]any{"body": "a\n--b"}}
	}},
}

// withMultipart replaces the body of the response with a multipart body, and returns the multipart object.
func withMultipart(doc map[string]any) map[string]any {
	response := sub(doc, "response")
	delete(response, "body")

	multipart := map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "x"}}}
	response["multipart"] = multipart

	return multipart
}

// schemaBlind reports the faults the editor schema can't express: matcher arguments are the decoder's alone, and no
// JSON Schema can tell whether a file exists, or whether a line of a body starts with the boundary.
func schemaBlind(f mockFault) bool {
	return f.area == "matcher" || f.code == config.CodeFileNotFound || f.key == "response.multipart.parts[0].body"
}

// sub returns the object under key, creating it.
func sub(doc map[string]any, key string) map[string]any {
	obj, ok := doc[key].(map[string]any)
	if !ok {
		obj = make(map[string]any)
		doc[key] = obj
	}

	return obj
}

func TestPropMockFile_EveryFaultIsReported(t *testing.T) {
	// M3: each fault is reported once, with its code and key, and nothing else is: a file is fixed in one pass.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		doc := map[string]any{
			"request":  map[string]any{"method": "GET", "path": "/" + genToken().Draw(t, "path")},
			"response": map[string]any{"status": int64(200), "body": genToken().Draw(t, "body")},
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_mockFaults), 1, len(_mockFaults),
			func(f mockFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(doc)
			want = append(want, f.code+" "+f.key)
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		_, err := mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}.build(m)

		got := make([]string, 0, len(want))
		for _, p := range mockProblems(t, err) {
			require.Equal(t, "doc", p.Origin)
			got = append(got, p.Code+" "+p.Key)
		}

		require.ElementsMatch(t, want, got)
	})
}

// mutate changes one random place of a document: it deletes, renames, retypes, wraps or adds a key.
func mutate(t *rapid.T, doc map[string]any) {
	// Collect the objects of the document and pick one to change.
	var objects []map[string]any

	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			objects = append(objects, x)
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)

	obj := rapid.SampledFrom(objects).Draw(t, "object")
	keys := slices.Sorted(maps.Keys(obj))

	op := rapid.IntRange(0, 4).Draw(t, "mutation")
	if len(keys) == 0 {
		op = 4
	}

	switch op {
	case 0:
		delete(obj, rapid.SampledFrom(keys).Draw(t, "deleted"))
	case 1:
		k := rapid.SampledFrom(keys).Draw(t, "renamed")
		obj[k+rapid.SampledFrom([]string{"x", "_", "s"}).Draw(t, "suffix")] = obj[k]
		delete(obj, k)
	case 2:
		obj[rapid.SampledFrom(keys).Draw(t, "retyped")] = genAny(2).Draw(t, "new value")
	case 3:
		k := rapid.SampledFrom(keys).Draw(t, "wrapped")
		obj[k] = []any{obj[k]}
	default:
		obj[rapid.SampledFrom([]string{"status", "body", "header", "path", "method", "query", "x", "responses", "target", "data"}).Draw(t, "added")] = genAny(2).Draw(t, "added value")
	}
}

// genAny draws any value of a document.
func genAny(depth int) *rapid.Generator[any] {
	gens := []*rapid.Generator[any]{
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.Int64Range(-1000, 1000), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-10, 10), func(f float64) any { return f }),
		rapid.Map(rapid.SampledFrom([]string{"", "x", "gzip", "GET", "/x", "equal", "present", "[", "http://localhost", "1s", "-1"}), func(s string) any { return s }),
	}

	if depth > 0 {
		gens = append(gens,
			rapid.Map(rapid.SliceOfN(rapid.Deferred(func() *rapid.Generator[any] { return genAny(depth - 1) }), 0, 3), func(s []any) any { return s }),
			rapid.Map(rapid.MapOfN(rapid.SampledFrom([]string{"a", "status", "equal", "name"}), rapid.Deferred(func() *rapid.Generator[any] { return genAny(depth - 1) }), 0, 3),
				func(m map[string]any) any { return m }),
		)
	}

	return rapid.OneOf(gens...)
}

// logBuffer collects the server's logs, safe for concurrent writes.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestPropMockFile_MutationsNeverPanic(t *testing.T) {
	// M4: whatever a document turns into, building it gives a mock or a list of problems, never a panic;
	// and a mock built from it never panics serving a request.
	t.Parallel()

	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("x", stop))
	m.MustStart()
	t.Cleanup(m.Close)

	var built, failed int

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock(m.URL()).Draw(t, "mock").doc()
		for range rapid.IntRange(1, 4).Draw(t, "mutations") {
			mutate(t, doc)
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}})
		if err != nil {
			_, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "a mock document fails with its problems, not %v", err)

			failed++

			return
		}

		built++

		defer scope.Clear(context.Background())

		req := requestSpec{
			method: rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:   rapid.SampledFrom([]string{"/", "/x", "/" + genToken().Draw(t, "path")}).Draw(t, "path"),
			header: http.Header{"X-Id": {genToken().Draw(t, "header")}},
		}

		if req.method == http.MethodPost {
			body := rapid.SampledFrom([]string{"", "x", `{"a": 1}`}).Draw(t, "body")
			req.body = &body
		}

		send(t, m.URL(), req)

		require.NotContains(t, logs.String(), "panic", "serving %v", doc)
	})

	t.Logf("built %d mocks, rejected %d documents", built, failed)
	require.Positive(t, built, "the property must serve built mocks, not only reject documents")
	require.Positive(t, failed, "the property must reject broken documents, not only build them")
}

func FuzzMockDoc(f *testing.F) {
	// M14: no document panics the builder; every failure is a mock *config.Error.
	seeds, err := filepath.Glob("testdata/*/*.*")
	require.NoError(f, err)

	more, err := filepath.Glob("e2e/testdata/*/*.*")
	require.NoError(f, err)

	for _, path := range append(seeds, more...) {
		format, ok := mockFileFormat(filepath.Ext(path))
		if !ok {
			continue
		}

		data, err := os.ReadFile(path)
		require.NoError(f, err)

		f.Add(data, format)
	}

	m := MustNew(Setup().Silent().OnCompleteListener("webhook", stop))

	f.Fuzz(func(t *testing.T, data []byte, format string) {
		_, err := mockDoc{origin: "fuzz", format: format, data: data}.build(m)
		if err == nil {
			return
		}

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)
		require.Equal(t, "mock", cfgErr.Scope)

		for _, p := range cfgErr.Problems {
			require.NotEmpty(t, p.Code)
			require.NotEmpty(t, p.Origin)
			require.NotEmpty(t, p.Message)
		}
	})
}
