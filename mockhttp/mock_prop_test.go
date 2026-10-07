package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// docBuilder builds a mock from a document, without the template pass, as the admin API does.
type docBuilder struct {
	doc mockDoc
}

func (b docBuilder) Build(app *Server) (*Mock, error) {
	return b.doc.build(app)
}

// send sends a request and reads its whole response.
func send(t require.TestingT, base string, req requestSpec) (*http.Response, []byte) {
	u := base + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}

	var body io.Reader

	contentType := ""

	switch {
	case req.multipart:
		form := formModel{files: req.files}
		for _, k := range slices.Sorted(maps.Keys(req.form)) {
			for _, v := range req.form[k] {
				form.fields = append(form.fields, formField{name: k, value: v})
			}
		}

		var b []byte
		contentType, b = form.encode(t)
		body = bytes.NewReader(b)
	case req.form != nil:
		body = strings.NewReader(req.form.Encode())
		contentType = MIMEFormURLEncoded
	case req.body != nil:
		body = strings.NewReader(*req.body)
		contentType = MIMETextPlain
	}

	r, err := http.NewRequest(req.method, u, body)
	require.NoError(t, err)

	for k, vv := range req.header {
		for _, v := range vv {
			r.Header.Add(k, v)
		}
	}

	if contentType != "" {
		r.Header.Set(HeaderContentType, contentType)
	}

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// replyDiff says how a response differs from what a reply model predicts, or "" when it doesn't.
func replyDiff(want replyModel, method string, res *http.Response, body []byte) string {
	if res.StatusCode != want.status {
		return fmt.Sprintf("status %d, want %d", res.StatusCode, want.status)
	}

	byName := make(map[string][]string)
	for _, h := range want.header {
		byName[h.name] = append(byName[h.name], h.value)
	}

	for name, values := range byName {
		if got := res.Header.Values(name); !slicesEqual(got, values) {
			return fmt.Sprintf("header %s %q, want %q", name, got, values)
		}
	}

	for _, name := range want.template {
		if got := res.Header.Get(name); got != method {
			return fmt.Sprintf("templated header %s %q, want the method %q", name, got, method)
		}
	}

	for _, h := range want.trailer {
		if got := res.Trailer.Get(h.name); got != h.value {
			return fmt.Sprintf("trailer %s %q, want %q", h.name, got, h.value)
		}
	}

	switch {
	case want.multipart != nil:
		mediaType, _, err := mime.ParseMediaType(res.Header.Get(HeaderContentType))
		if err != nil || mediaType != "multipart/"+strings.ToLower(want.multipart.subtype) {
			return fmt.Sprintf("content type %q, want multipart/%s", res.Header.Get(HeaderContentType), want.multipart.subtype)
		}

		if got := readMultipartParts(res.Header.Get(HeaderContentType), body); !reflect.DeepEqual(got, want.multipart.want()) {
			return fmt.Sprintf("multipart parts %v, want %v", got, want.multipart.want())
		}
	case want.text != nil:
		if string(body) != *want.text {
			return fmt.Sprintf("body %q, want %q", body, *want.text)
		}
	case want.data != nil:
		if string(body) != *want.data {
			return fmt.Sprintf("templated body %q, want %q", body, *want.data)
		}
	case want.hasJSON:
		// The body is the value as deterministic JSON: same value, same text.
		wantJSON, err := json.Marshal(want.json, json.Deterministic(true))
		if err != nil {
			return err.Error()
		}

		if string(body) != string(wantJSON) {
			return fmt.Sprintf("JSON body %s, want %s", body, wantJSON)
		}
	default:
		if len(body) != 0 {
			return fmt.Sprintf("body %q, want none", body)
		}
	}

	return ""
}

func slicesEqual(a, b []string) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func TestPropMockFile_ServesItsModel(t *testing.T) {
	// M2: a mock file means what it says. A request built to match gets the modeled replies, in order and
	// within repeat; a request that differs in any one matched part gets the no-match status.
	// M18: that holds for multipart forms too: their fields, and the filename, content type and content of a file,
	// whatever boundary the client picks.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		model := genMock(m.URL()).Draw(t, "mock")
		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "model", format: format, data: encodeMock(t, model.doc(), format)}})
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		// Mismatches first: they must not count towards repeat.
		for _, req := range model.request.mismatches(t) {
			res, _ := send(t, m.URL(), req)
			require.Equal(t, StatusNoMatch, res.StatusCode, "a request with another %s must not match", req.what)
		}

		require.Zero(t, scope.Hits())

		if !model.enabled {
			res, _ := send(t, m.URL(), model.request.matching(t))
			require.Equal(t, StatusNoMatch, res.StatusCode, "a disabled mock must not match")

			return
		}

		served := model.served()
		for i, want := range served {
			req := model.request.matching(t)
			start := time.Now()
			res, body := send(t, m.URL(), req)

			if i == 0 {
				require.GreaterOrEqual(t, time.Since(start), model.delay, "delay")
			}

			if model.random != nil {
				diffs := make([]string, 0, len(model.random))
				for _, r := range model.random {
					diffs = append(diffs, replyDiff(r, req.method, res, body))
				}

				require.Contains(t, diffs, "", "random reply %d is none of the replies: %v", i, diffs)

				continue
			}

			require.Empty(t, replyDiff(want, req.method, res, body), "reply %d of %d", i+1, len(served))
		}

		if model.repeat > 0 && int64(len(served)) == model.repeat {
			res, _ := send(t, m.URL(), model.request.matching(t))
			require.Equal(t, StatusNoMatch, res.StatusCode, "the mock was served %d times, as many as repeat", model.repeat)
		}
	})
}

func TestPropMockFile_FormatsBuildTheSameMock(t *testing.T) {
	// M1: the same mock written as JSON and as YAML builds the same mock.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock(m.URL()).Draw(t, "mock").doc()
		if _, ok := doc["id"]; !ok {
			doc["id"] = "same" // generated IDs differ; the rest must not
		}

		described := make([]string, 0, len(MockFileFormats()))
		for _, format := range MockFileFormats() {
			mock, err := mockDoc{origin: format, format: format, data: encodeMock(t, doc, format)}.build(m)
			require.NoError(t, err, format)

			described = append(described, describe(t, mock))
		}

		require.Equal(t, described[0], described[1], "json and yaml")
		require.Equal(t, described[0], described[2], "json and yml")
	})
}

func TestPropMockFile_BuildIsRepeatable(t *testing.T) {
	// M5: a builder builds a new mock each time, equal to the last, with its own state.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock("http://localhost").Draw(t, "mock").doc()
		doc["id"] = "same"

		b := docBuilder{mockDoc{origin: "doc", format: "json", data: encodeMock(t, doc, "json")}}

		first, err := b.Build(m)
		require.NoError(t, err)
		second, err := b.Build(m)
		require.NoError(t, err)

		require.NotSame(t, first, second)
		require.Equal(t, describe(t, first), describe(t, second))

		first.Inc()
		require.Zero(t, second.Hits())
	})
}

func TestPropMockFile_DurationsAreTheSameEverywhere(t *testing.T) {
	// M6: delay and proxy.timeout read the same value the same way: integers are milliseconds (C2).
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var raw any = rapid.Int64Range(0, 1<<40).Draw(t, "ms")
		if rapid.Bool().Draw(t, "as a Go duration") {
			raw = time.Duration(rapid.Int64Range(0, 1<<50).Draw(t, "ns")).String()
		}

		doc := map[string]any{
			"request": map[string]any{"path": "/x"},
			"delay":   raw,
			"proxy":   map[string]any{"target": "http://localhost", "timeout": raw},
		}

		def := newMockDef()
		_, err := _mockSchema.Load(&def, []config.Source{config.Bytes("doc", "yaml", encodeMock(t, doc, "yaml"))})
		require.NoError(t, err)

		require.Equal(t, def.delay, def.proxy.timeout)

		if ms, ok := raw.(int64); ok {
			require.Equal(t, time.Duration(ms)*time.Millisecond, def.delay)
		}
	})
}

func TestPropMockFile_IDRule(t *testing.T) {
	// M7: a file's id is accepted iff it follows the rule, and becomes the mock's ID exactly.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.OneOf(rapid.StringMatching(`[A-Za-z0-9._~-]{1,12}`), rapid.String()).Draw(t, "id")
		doc := map[string]any{"id": id, "request": map[string]any{"path": "/x"}}

		mock, err := mockDoc{origin: "doc", format: "json", data: encodeMock(t, doc, "json")}.build(m)
		if _idOracle.MatchString(id) {
			require.NoError(t, err)
			require.Equal(t, id, mock.ID())

			return
		}

		problems := mockProblems(t, err)
		require.Len(t, problems, 1)
		require.Equal(t, "id", problems[0].Key)
	})
}

func TestPropMockFile_TemplatesLeavePlainDocumentsAlone(t *testing.T) {
	// M9: a document without template actions builds the same mock with or without the template pass.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock("http://localhost").Draw(t, "mock").doc()
		doc["id"] = "same"

		data := encodeMock(t, doc, "yaml")
		if strings.Contains(string(data), "{{") {
			t.Skip("the document holds template actions")
		}

		plain, err := mockDoc{origin: "doc", format: "yaml", data: data}.build(m)
		require.NoError(t, err)
		templated, err := FromBytes(data, "yaml").Build(m)
		require.NoError(t, err)

		require.Equal(t, describe(t, plain), describe(t, templated))
	})
}

var errListener = errors.New("listener stopped")

// stop is a listener that never runs: a mock needs its on_complete listeners registered.
var stop = vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return errListener })

// readMultipartParts reads the parts of a multipart body as a client does, or nil when it doesn't read.
func readMultipartParts(contentType string, body []byte) []readPart {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil
	}

	var parts []readPart

	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			return parts
		}

		if err != nil {
			return nil
		}

		b, err := io.ReadAll(part)
		if err != nil {
			return nil
		}

		parts = append(parts, readPart{header: part.Header, body: string(b)})
	}
}
