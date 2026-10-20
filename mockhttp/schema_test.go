package mockhttp

import (
	"bytes"
	json "encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mpart"
)

// http.schema.json is for editors: the decoder doesn't read it. These tests keep the two in step.

const _schemaURL = "mem://vlatte/mock.schema.json"

func loadJSONSchema(t require.TestingT) (*jsonschema.Schema, map[string]any) {
	b, err := os.ReadFile("schema/http.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	require.NoError(t, c.AddResource(_schemaURL, doc))

	schema, err := c.Compile(_schemaURL)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))

	return schema, raw
}

// instance converts a document to what the JSON Schema validator takes.
func instance(t require.TestingT, doc any) any {
	b, err := json.Marshal(doc)
	require.NoError(t, err)

	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	return v
}

func TestJSONSchema_KeysMatchTheTables(t *testing.T) {
	// Every key the decoder reads is documented for editors, and nothing else is.
	_, raw := loadJSONSchema(t)

	props := func(node any) map[string]any {
		obj, _ := node.(map[string]any)
		p, _ := obj["properties"].(map[string]any)

		return p
	}

	// keys lists the properties of an object schema, and those of the groups named, as dotted keys.
	keys := func(node any, groups ...string) []string {
		var out []string
		for k, v := range props(node) {
			out = append(out, k)
			if slices.Contains(groups, k) {
				for sub := range props(v) {
					out = append(out, k+"."+sub)
				}
			}
		}

		slices.Sort(out)

		return out
	}

	defs, _ := raw["$defs"].(map[string]any)

	require.Equal(t, _mockSchema.Keys(), keys(raw, "scenario", "request"), "mock")
	require.Equal(t, _replySchema.Keys(), keys(defs["reply"], "template"), "reply")
	require.Equal(t, _sequenceSchema.Keys(), keys(props(raw)["response_sequence"]), "response_sequence")
	require.Equal(t, _randomSchema.Keys(), keys(props(raw)["response_random"]), "response_random")
	require.Equal(t, _proxySchema.Keys(), keys(props(raw)["proxy"]), "proxy")
	require.Equal(t, _eventSchema.Keys(), keys(defs["on_complete_event"]), "on_complete")
	require.Equal(t, _filePatternSchema.Keys(), keys(defs["file_pattern"]), "form_file")
	require.Equal(t, mpart.SpecSchema.Keys(), keys(defs["multipart"]), "multipart")
	require.Equal(t, mpart.PartSchema.Keys(), keys(defs["multipart_part"]), "multipart part")
}

func TestPropJSONSchema_AgreesWithTheDecoder(t *testing.T) {
	// M11: the editor schema accepts every mock the decoder builds, and rejects the faults it can express.
	// The arguments of matchers are the decoder's alone.
	t.Parallel()

	schema, _ := loadJSONSchema(t)

	expressible := slices.DeleteFunc(slices.Clone(_mockFaults), schemaBlind)

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock("http://localhost:8080").Draw(t, "mock").doc()
		require.NoError(t, schema.Validate(instance(t, doc)), "a mock the decoder builds")

		// A proxy is a fault only next to another reply.
		if doc["response"] == nil && doc["response_sequence"] == nil && doc["response_random"] == nil {
			doc["response"] = map[string]any{"status": int64(200)}
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(expressible), 1, 3, func(f mockFault) string { return f.area }).Draw(t, "faults")
		for _, f := range faults {
			f.inject(doc)
		}

		require.Error(t, schema.Validate(instance(t, doc)), "faults %v", faults)
	})
}

func TestJSONSchema_AcceptsTheFixturesTheDecoderAccepts(t *testing.T) {
	// An editor must not mark a working mock file as wrong.
	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent().OnCompleteListener("webhook", stop))

	var files []string
	for _, pattern := range []string{"testdata/*/*.*", "testdata/*/*/*.*", "e2e/testdata/*/*.*", "../webhook/testdata/*.*"} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)

		files = append(files, matches...)
	}

	checked := 0

	for _, file := range files {
		format, ok := mockFileFormat(filepath.Ext(file))
		if !ok {
			continue
		}

		data, err := os.ReadFile(file)
		require.NoError(t, err)

		// dir, as FromFile sets it: without it, fixtures with relative files would be skipped as wrong.
		if _, err := (mockDoc{origin: file, format: format, dir: filepath.Dir(file), data: data}).build(m); err != nil {
			continue // wrong on purpose, or not a mock file
		}

		var doc any
		require.NoError(t, yaml.Unmarshal(data, &doc), file)
		require.NoError(t, schema.Validate(instance(t, doc)), file)

		checked++
	}

	require.Greater(t, checked, 20, "the fixtures are checked")
}

func TestMockFileFormats_AdminAcceptsTheSame(t *testing.T) {
	// A mock file in any format can be posted to the admin API, and nothing else can.
	require.ElementsMatch(t, MockFileFormats(), slices.Collect(maps.Keys(admin.SupportedExtensions)))
}

// _scalarKeys places a value at each key of the mock format that takes a scalar kind, in a document that is
// valid with the key's valid value.
var _scalarKeys = []struct {
	key   string
	valid any
	doc   func(v any) map[string]any
}{
	{"id", "users", topLevel("id")},
	{"name", "users", topLevel("name")},
	{"priority", int64(1), topLevel("priority")},
	{"enabled", true, topLevel("enabled")},
	{"delay", "1s", topLevel("delay")},
	{"repeat", int64(2), topLevel("repeat")},
	{"scenario.name", "s", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": v}})
	}},
	{"scenario.required_state", "a", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": "s", "required_state": v}})
	}},
	{"scenario.new_state", "b", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": "s", "new_state": v}})
	}},
	{"request.url_match", "x", func(v any) map[string]any { return map[string]any{"request": map[string]any{"url_match": v}} }},
	{"response.status", int64(200), inReply("status")},
	{"response.body_file", "b.txt", inReply("body_file")},
	{"response.encoding", "gzip", inReply("encoding")},
	{"response.template.enabled", true, func(v any) map[string]any {
		return withRequest(map[string]any{"response": map[string]any{"template": map[string]any{"enabled": v}}})
	}},
	{"response_random.seed", int64(1), func(v any) map[string]any {
		return withRequest(map[string]any{"response_random": map[string]any{"responses": []any{make(map[string]any)}, "seed": v}})
	}},
	{"proxy.target", "http://a", func(v any) map[string]any { return withRequest(map[string]any{"proxy": map[string]any{"target": v}}) }},
	{"proxy.remove_headers", []any{"a"}, inProxy("remove_headers")},
	{"proxy.trim_prefix", "/a", inProxy("trim_prefix")},
	{"proxy.trim_suffix", "/b", inProxy("trim_suffix")},
	{"proxy.timeout", int64(10), inProxy("timeout")},
	{"proxy.ssl_verify", true, inProxy("ssl_verify")},
	{"proxy.x_forwarded", true, inProxy("x_forwarded")},
	{"proxy.preserve_host", true, inProxy("preserve_host")},
	{"on_complete.name", "hook", func(v any) map[string]any {
		return withRequest(map[string]any{"on_complete": []any{map[string]any{"name": v}}})
	}},
	{"request.form_file.f.every", true, func(v any) map[string]any {
		return map[string]any{"request": map[string]any{"form_file": map[string]any{"f": map[string]any{"every": v}}}}
	}},
	{"response.multipart.type", "mixed", inMultipart(func(v any) map[string]any {
		return map[string]any{"type": v, "parts": []any{map[string]any{"body": "x"}}}
	})},
	{"response.multipart.boundary", "b0", inMultipart(func(v any) map[string]any {
		return map[string]any{"type": "mixed", "boundary": v, "parts": []any{map[string]any{"body": "x"}}}
	})},
	{"response.multipart.parts[0].name", "a", inPart("name")},
	{"response.multipart.parts[0].filename", "a.png", inPart("filename")},
	{"response.multipart.parts[0].content_type", "image/png", inPart("content_type")},
	{"response.multipart.parts[0].body_file", "a.png", inPart("body_file")},
	{"response.multipart.parts[0].delay", "1s", inPart("delay")},
}

func inMultipart(multipart func(v any) map[string]any) func(v any) map[string]any {
	return func(v any) map[string]any {
		return withRequest(map[string]any{"response": map[string]any{"multipart": multipart(v)}})
	}
}

func inPart(key string) func(v any) map[string]any {
	return inMultipart(func(v any) map[string]any {
		return map[string]any{"type": "mixed", "parts": []any{map[string]any{key: v}}}
	})
}

func withRequest(doc map[string]any) map[string]any {
	doc["request"] = map[string]any{"path": "/x"}
	return doc
}

func topLevel(key string) func(v any) map[string]any {
	return func(v any) map[string]any { return withRequest(map[string]any{key: v}) }
}

func inReply(key string) func(v any) map[string]any {
	return func(v any) map[string]any { return withRequest(map[string]any{"response": map[string]any{key: v}}) }
}

func inProxy(key string) func(v any) map[string]any {
	return func(v any) map[string]any {
		return withRequest(map[string]any{"proxy": map[string]any{"target": "http://a", key: v}})
	}
}

// _substitutes are values of every class, and text that reads as other kinds, put in place of a valid value.
var _substitutes = []any{
	nil, true, "true", "t", "yes", int64(0), int64(1), int64(5), "5", 1.5, "1.5s",
	[]any{},
	[]any{"a"},
	[]any{int64(1)},
	make(map[string]any),
}

func TestJSONSchema_ScalarTypesAgreeWithTheDecoder(t *testing.T) {
	// M16: for every key that takes a scalar kind, the editor schema accepts a value exactly when the decoder does,
	// so an editor never marks a working mock file as wrong, nor misses a value the decoder rejects (Codex review:
	// the decoder took name: 123, which editors reject).
	schema, _ := loadJSONSchema(t)

	setup := Setup().Silent().OnCompleteListener("hook", stop)
	for _, v := range _substitutes {
		if s, ok := v.(string); ok {
			setup.OnCompleteListener(s, stop)
		}
	}

	m := MustNew(setup)

	covered := make([]string, 0, len(_scalarKeys))
	for _, row := range _scalarKeys {
		covered = append(covered, row.key)

		for i, v := range append([]any{row.valid}, _substitutes...) {
			doc := row.doc(v)

			data, err := config.Encode(doc, "json")
			require.NoError(t, err)

			_, decodeErr := mockDoc{origin: "doc", format: "json", data: data}.build(m)
			schemaErr := schema.Validate(instance(t, doc))

			require.Equal(t, decodeErr == nil, schemaErr == nil, "%s: %#v\ndecoder: %v\nschema: %v", row.key, v, decodeErr, schemaErr)

			if i == 0 {
				require.NoError(t, decodeErr, "%s takes %#v", row.key, v)
			}
		}
	}

	// Every key of a scalar kind has a row.
	var scalar []string
	for prefix, keys := range map[string]func() ([]string, func(string) (config.Kind, bool)){
		"":                 func() ([]string, func(string) (config.Kind, bool)) { return _mockSchema.Keys(), _mockSchema.KindOf },
		"response.":        func() ([]string, func(string) (config.Kind, bool)) { return _replySchema.Keys(), _replySchema.KindOf },
		"response_random.": func() ([]string, func(string) (config.Kind, bool)) { return _randomSchema.Keys(), _randomSchema.KindOf },
		"response_sequence.": func() ([]string, func(string) (config.Kind, bool)) {
			return _sequenceSchema.Keys(), _sequenceSchema.KindOf
		},
		"proxy.":       func() ([]string, func(string) (config.Kind, bool)) { return _proxySchema.Keys(), _proxySchema.KindOf },
		"on_complete.": func() ([]string, func(string) (config.Kind, bool)) { return _eventSchema.Keys(), _eventSchema.KindOf },
		"request.form_file.f.": func() ([]string, func(string) (config.Kind, bool)) {
			return _filePatternSchema.Keys(), _filePatternSchema.KindOf
		},
		"response.multipart.": func() ([]string, func(string) (config.Kind, bool)) {
			return mpart.SpecSchema.Keys(), mpart.SpecSchema.KindOf
		},
		"response.multipart.parts[0].": func() ([]string, func(string) (config.Kind, bool)) {
			return mpart.PartSchema.Keys(), mpart.PartSchema.KindOf
		},
	} {
		all, kindOf := keys()
		for _, key := range all {
			// A pattern's body_file must name a file that exists, which no JSON Schema can check: the rule table
			// pins its type, and the fault catalogue its file_not_found.
			if prefix+key == "request.form_file.f.body_file" {
				continue
			}

			if kind, ok := kindOf(key); ok && kind != config.Any {
				scalar = append(scalar, prefix+key)
			}
		}
	}

	require.ElementsMatch(t, scalar, covered)
}

// genDurationText draws strings near Go's duration syntax: one or more components, signs, odd units and forms
// such as 1. and .5. Values stay small, so none overflows a time.Duration.
func genDurationText() *rapid.Generator[string] {
	component := rapid.Custom(func(t *rapid.T) string {
		number := rapid.OneOf(
			rapid.StringMatching(`[0-9]{1,3}`),
			rapid.StringMatching(`[0-9]{1,3}\.[0-9]{0,2}`),
			rapid.StringMatching(`\.[0-9]{1,2}`),
			rapid.Just("."),
		).Draw(t, "number")

		unit := rapid.SampledFrom([]string{"ns", "us", "µs", "μs", "ms", "s", "m", "h", "", "x", "mss", " s", "S"}).Draw(t, "unit")

		return number + unit
	})

	return rapid.OneOf(
		rapid.Custom(func(t *rapid.T) string {
			sign := rapid.SampledFrom([]string{"", "", "", "+", "-"}).Draw(t, "sign")
			return sign + strings.Join(rapid.SliceOfN(component, 1, 3).Draw(t, "components"), "")
		}),
		rapid.SampledFrom([]string{"0", "00", "-0", "+0", "", ".s", "1.s", "0s"}),
	)
}

func TestPropJSONSchema_DurationPattern(t *testing.T) {
	// M17: the editor's duration pattern, the decoder and time.ParseDuration agree on every string:
	// it is an unsigned Go duration, or it is wrong for all three.
	t.Parallel()

	_, raw := loadJSONSchema(t)

	defs, _ := raw["$defs"].(map[string]any)
	duration, _ := defs["duration"].(map[string]any)
	forms, _ := duration["oneOf"].([]any)
	text, _ := forms[1].(map[string]any)
	source, _ := text["pattern"].(string)
	pattern := regexp.MustCompile(source)

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		s := genDurationText().Draw(t, "duration")

		_, err := time.ParseDuration(s)
		want := err == nil && !strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "-")

		require.Equal(t, want, pattern.MatchString(s), "pattern on %q", s)

		data, err := config.Encode(withRequest(map[string]any{"delay": s}), "json")
		require.NoError(t, err)

		_, err = mockDoc{origin: "doc", format: "json", data: data}.build(m)
		require.Equal(t, want, err == nil, "decoder on %q: %v", s, err)
	})
}

func TestJSONSchema_ProxyTargetPattern(t *testing.T) {
	// #63: the editor's pattern for proxy.target took any scheme, which the decoder refuses. Both take an absolute http
	// or https URL only, in any case.
	_, raw := loadJSONSchema(t)

	props, _ := raw["properties"].(map[string]any)
	proxy, _ := props["proxy"].(map[string]any)
	proxyProps, _ := proxy["properties"].(map[string]any)
	target, _ := proxyProps["target"].(map[string]any)
	source, _ := target["pattern"].(string)
	require.NotEmpty(t, source, "proxy.target has no pattern")

	pattern := regexp.MustCompile(source)

	for target, want := range map[string]bool{
		"http://api.local:8080": true,
		"https://api.local/v1":  true,
		"HTTPS://api.local":     true,
		"ftp://files.local":     false,
		"ws://api.local":        false,
		"/x":                    false,
		"https://":              false,
		"mailto:a@b":            false,
	} {
		_, err := checkTargetURL(target)
		require.Equal(t, want, err == nil, "decoder on %q: %v", target, err)
		require.Equal(t, want, pattern.MatchString(target), "pattern on %q", target)
	}
}
