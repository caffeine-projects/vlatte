package mockhttp

import (
	"bytes"
	json "encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/admin"
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
}

func TestPropJSONSchema_AgreesWithTheDecoder(t *testing.T) {
	// M11: the editor schema accepts every mock the decoder builds, and rejects the faults it can express.
	// The arguments of matchers are the decoder's alone.
	schema, _ := loadJSONSchema(t)

	expressible := slices.DeleteFunc(slices.Clone(_mockFaults), func(f mockFault) bool { return f.area == "matcher" })

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

		if strings.Contains(string(data), "{{") {
			continue // a template, valid once rendered
		}

		if _, err := (mockDoc{origin: file, format: format, data: data}).build(m); err != nil {
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
