package webhook

import (
	"bytes"
	json "encoding/json/v2"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// loadSchema compiles the JSON schema of mock files, and returns it with its document.
func loadSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()

	b, err := os.ReadFile("../mockhttp/schema/http.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource("mem://vlatte/http.schema.json", doc))

	schema, err := c.Compile("mem://vlatte/http.schema.json")
	require.NoError(t, err)

	raw, ok := doc.(map[string]any)
	require.True(t, ok)

	return schema, raw
}

func TestJSONSchema_WebhookParametersAreTheDecodersKeys(t *testing.T) {
	// Editors check the parameters of webhooks with the schema of mock files: it documents every key the webhook
	// decodes, and nothing else.
	_, raw := loadSchema(t)

	defs, _ := raw["$defs"].(map[string]any)
	params, _ := defs["webhook_parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)

	require.Equal(t, _argsSchema.Keys(), slices.Sorted(maps.Keys(props)))
}

func TestJSONSchema_AcceptsWhatPrepareAccepts(t *testing.T) {
	// An editor marks a webhook's parameters as wrong when building its mock fails, but for what only building the mock
	// finds: header names equal but for case, as JSON Schema can't compare property names that way, and the syntax of a
	// url past its scheme and host, which a pattern can't check as url.Parse does.
	schema, _ := loadSchema(t)
	engine := vlatte.MustNewGoTemplateEngine()
	multipart := map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "a"}}}

	tests := []struct {
		name        string
		params      map[string]any
		prepareOnly bool // building the mock rejects it, and JSON Schema can't tell
	}{
		{name: "a url", params: map[string]any{"url": "http://hooks.test/x"}},
		{name: "every kind of key", params: map[string]any{
			"url_template": "http://hooks.test/{{ .Request.Method }}", "method": "post",
			"header": map[string]any{"X-A": "1"}, "header_template": map[string]any{"X-B": "{{ .Request.Method }}"},
			"body_template": "{{ .Request.Method }}", "delay": "1.5s",
		}},
		{name: "a multipart body", params: map[string]any{"url": "http://hooks.test", "multipart": multipart}},
		{name: "a delay in milliseconds", params: map[string]any{"url": "http://hooks.test", "delay": 1500}},
		{name: "no url", params: map[string]any{"method": "POST"}},
		{name: "url and url_template", params: map[string]any{"url": "http://hooks.test", "url_template": "http://hooks.test"}},
		{name: "a relative url", params: map[string]any{"url": "/hooks"}},
		{name: "an ftp url", params: map[string]any{"url": "ftp://files.test"}},
		{name: "a method that isn't one", params: map[string]any{"url": "http://hooks.test", "method": "ghjk%&^*()"}},
		{name: "two bodies", params: map[string]any{"url": "http://hooks.test", "body": "x", "body_file": "x.txt"}},
		{name: "a multipart body and a Content-Type header", params: map[string]any{
			"url": "http://hooks.test", "header": map[string]any{"content-type": "x"}, "multipart": multipart,
		}},
		{name: "a list as a header value", params: map[string]any{"url": "http://hooks.test", "header": map[string]any{"X-A": []any{"1", "2"}}}},
		{name: "a list holding a number as a header value", params: map[string]any{
			"url": "http://hooks.test", "header_template": map[string]any{"X-A": []any{"1", 2}},
		}},
		{name: "a number as a header value", params: map[string]any{"url": "http://hooks.test", "header": map[string]any{"X-A": 1}}},
		{name: "a negative delay", params: map[string]any{"url": "http://hooks.test", "delay": -1}},
		{name: "a key that doesn't exist", params: map[string]any{"url": "http://hooks.test", "urll": "x"}},
		{name: "an uppercase scheme", params: map[string]any{"url": "HTTP://hooks.test"}},
		{name: "a mixed-case scheme", params: map[string]any{"url": "Https://hooks.test/x"}},
		{name: "a host with an unclosed bracket", params: map[string]any{"url": "http://[::1"}, prepareOnly: true},
		{name: "a port that isn't a number", params: map[string]any{"url": "http://hooks.test:port"}, prepareOnly: true},
		{name: "header names equal but for case", params: map[string]any{
			"url": "http://hooks.test", "header": map[string]any{"X-A": "1", "x-a": "2"},
		}, prepareOnly: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, prepareErr := (&WebHook{}).Prepare(vlatte.EventConfig{Name: "webhook", Parameters: tc.params}, engine)

			doc, err := json.Marshal(map[string]any{
				"request":     map[string]any{"path": "/x"},
				"response":    map[string]any{"status": 204},
				"on_complete": []any{map[string]any{"name": "webhook", "parameters": tc.params}},
			})
			require.NoError(t, err)

			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
			require.NoError(t, err)

			schemaErr := schema.Validate(instance)
			if tc.prepareOnly {
				require.Error(t, prepareErr)
				require.NoError(t, schemaErr)

				return
			}

			require.Equal(t, prepareErr == nil, schemaErr == nil, "Prepare: %v\nschema: %v", prepareErr, schemaErr)
		})
	}
}
