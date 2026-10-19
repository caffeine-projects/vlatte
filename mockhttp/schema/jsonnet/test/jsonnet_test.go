package test

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/go-jsonnet"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestJSONNet(t *testing.T) {
	vm := jsonnet.MakeVM()
	fjn, err := os.Open("root.jsonnet")
	require.NoError(t, err)

	defer fjn.Close()

	b, err := io.ReadAll(fjn)
	require.NoError(t, err)

	j, err := vm.EvaluateAnonymousSnippet("root.jsonnet", string(b))
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(j))
	require.NoError(t, err)
	require.NoError(t, compileSchema(t).Validate(doc))

	fmt.Println(j)
}

// compileSchema compiles the JSON schema of mock files.
func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	b, err := os.ReadFile("../../http.schema.json")
	require.NoError(t, err)

	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource("mem://vlatte/http.schema.json", schemaDoc))

	schema, err := c.Compile("mem://vlatte/http.schema.json")
	require.NoError(t, err)

	return schema
}

func TestJSONNet_Webhook(t *testing.T) {
	// post_actions.libsonnet had a webhook() that wrote an empty object, and nothing imported it. webhook writes an
	// on_complete event of the webhook listener, whose parameters the schema checks.
	vm := jsonnet.MakeVM()
	schema := compileSchema(t)

	mock := func(params string) string {
		return "local v = import '../http.libsonnet'; " +
			"v.mock([v.request([v.req.get()]), v.response([v.res.ok()]), v.postActions([v.webhook(" + params + ")])])"
	}

	j, err := vm.EvaluateAnonymousSnippet("webhook.jsonnet", mock("{ url: 'http://localhost:8080/hook', delay: '1s' }"))
	require.NoError(t, err)
	require.JSONEq(t, `[{"name": "webhook", "parameters": {"url": "http://localhost:8080/hook", "delay": "1s"}}]`,
		onCompleteOf(t, j))

	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(j))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(doc))

	// A webhook needs a URL.
	j, err = vm.EvaluateAnonymousSnippet("webhook.jsonnet", mock("{ method: 'POST' }"))
	require.NoError(t, err)

	doc, err = jsonschema.UnmarshalJSON(strings.NewReader(j))
	require.NoError(t, err)
	require.Error(t, schema.Validate(doc))
}

// onCompleteOf returns the on_complete of the mock document j, as JSON.
func onCompleteOf(t *testing.T, j string) string {
	t.Helper()

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(j), &doc))

	b, err := json.Marshal(doc["on_complete"])
	require.NoError(t, err)

	return string(b)
}

func TestJSONNet_HeaderTemplate(t *testing.T) {
	// headerTemplate writes header_template: a header map would send the template text as it is.
	vm := jsonnet.MakeVM()

	j, err := vm.EvaluateAnonymousSnippet("header_template.jsonnet",
		"(import '../http.libsonnet').res.headerTemplate({ 'X-Method': '{{ .Request.Method }}' })")
	require.NoError(t, err)
	require.JSONEq(t, `{"header_template": {"X-Method": "{{ .Request.Method }}"}}`, j)
}

func TestJSONNet_HeaderNames(t *testing.T) {
	// Mocks written in jsonnet name CORS headers with these: browsers look for the names of the Fetch standard.
	vm := jsonnet.MakeVM()

	j, err := vm.EvaluateAnonymousSnippet("header.jsonnet", "import '../http_header.libsonnet'")
	require.NoError(t, err)

	var names map[string]string
	require.NoError(t, json.Unmarshal([]byte(j), &names))

	for constant, name := range map[string]string{
		"HeaderContentLength":                 "Content-Length",
		"HeaderAccessControlRequestMethod":    "Access-Control-Request-Method",
		"HeaderAccessControlRequestHeaders":   "Access-Control-Request-Headers",
		"HeaderAccessControlAllowOrigin":      "Access-Control-Allow-Origin",
		"HeaderAccessControlAllowMethods":     "Access-Control-Allow-Methods",
		"HeaderAccessControlAllowHeaders":     "Access-Control-Allow-Headers",
		"HeaderAccessControlAllowCredentials": "Access-Control-Allow-Credentials",
		"HeaderAccessControlExposeHeaders":    "Access-Control-Expose-Headers",
		"HeaderAccessControlMaxAge":           "Access-Control-Max-Age",
	} {
		require.Equal(t, name, names[constant], constant)
	}
}
