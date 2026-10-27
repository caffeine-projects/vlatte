package mockgrpc

import (
	"bytes"
	json "encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// grpc.schema.json is for editors: the decoder doesn't read it. These tests keep the two in step.

const _schemaURL = "mem://vlatte/grpc.schema.json"

func loadJSONSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()

	b, err := os.ReadFile("schema/grpc.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource(_schemaURL, doc))

	schema, err := c.Compile(_schemaURL)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))

	return schema, raw
}

func TestJSONSchema_KeysMatchTheTables(t *testing.T) {
	// Every key the decoder reads is documented for editors, and nothing else is.
	_, raw := loadJSONSchema(t)

	props := func(node any) map[string]any {
		obj, _ := node.(map[string]any)
		p, _ := obj["properties"].(map[string]any)

		return p
	}

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
	bidi := props(raw)["bidi"]
	rule := props(props(bidi)["on"].(map[string]any)["items"])

	require.Equal(t, _mockSchema.Keys(), keys(raw, "scenario", "request"), "mock")
	require.Equal(t, _replySchema.Keys(), keys(defs["reply"]), "reply")
	require.Equal(t, _statusSchema.Keys(), keys(defs["status"]), "status")
	require.Equal(t, _bidiSchema.Keys(), keys(bidi), "bidi")
	require.Equal(t, _ruleSchema.Keys(), slices.Sorted(maps.Keys(rule)), "bidi rule")
	require.Equal(t, _eventSchema.Keys(), keys(defs["on_complete_event"]), "on_complete")
	require.Equal(t, _proxySchema.Keys(), keys(props(raw)["proxy"]), "proxy")
}

func TestJSONSchema_AcceptsTheFixturesTheDecoderAccepts(t *testing.T) {
	// An editor must not mark a working mock file as wrong.
	schema, _ := loadJSONSchema(t)
	m := fileServer(t, Setup())

	files, err := filepath.Glob("testdata/mocks/*.*")
	require.NoError(t, err)

	checked := 0

	for _, file := range files {
		if filepath.Base(file) == "items.json" { // a body_file, not a mock
			continue
		}

		_, err := m.Mock(FromFile(file))
		require.NoError(t, err, file)

		format, _ := config.Format(filepath.Ext(file))
		data, err := os.ReadFile(file)
		require.NoError(t, err)

		doc, err := config.Parse(data, format)
		require.NoError(t, err)

		b, err := json.Marshal(doc)
		require.NoError(t, err)

		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		require.NoError(t, schema.Validate(v), file)

		checked++
	}

	require.Equal(t, 5, checked)
}

func TestJSONSchema_RejectsWhatTheDecoderRejects(t *testing.T) {
	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent())

	for name, doc := range map[string]string{
		"no method":            `{"request": {}, "response": {}}`,
		"no answer":            `{"request": {"method": "Greetings"}}`,
		"response and bidi":    `{"request": {"method": "Chat"}, "response": {}, "bidi": {}}`,
		"response and proxy":   `{"request": {"method": "Greetings"}, "response": {}, "proxy": {"target": "localhost:1"}}`,
		"a proxy to nowhere":   `{"request": {"method": "Greetings"}, "proxy": {}}`,
		"message and messages": `{"request": {"method": "Greetings"}, "response": {"message": {}, "messages": []}}`,
		"an unknown key":       `{"request": {"method": "Greetings"}, "response": {"body": "x"}}`,
		"a code out of range":  `{"request": {"method": "Greetings"}, "response": {"status": {"code": 17}}}`,
	} {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(doc)))
		require.NoError(t, err)
		require.Error(t, schema.Validate(v), name)

		_, err = FromBytes([]byte(doc), "json").Build(m)
		require.Error(t, err, "the decoder rejects it too: %s", name)
	}
}
