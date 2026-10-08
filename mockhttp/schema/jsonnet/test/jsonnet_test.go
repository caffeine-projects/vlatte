package test

import (
	"bytes"
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

	fschema, err := os.Open("../../http.schema.json")
	require.NoError(t, err)

	defer fschema.Close()

	b, err = io.ReadAll(fschema)
	require.NoError(t, err)

	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource("mem://vlatte/http.schema.json", schemaDoc))

	schema, err := c.Compile("mem://vlatte/http.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(j))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(doc))

	fmt.Println(j)
}

func TestJSONNet_HeaderTemplate(t *testing.T) {
	// headerTemplate writes header_template: a header map would send the template text as it is.
	vm := jsonnet.MakeVM()

	j, err := vm.EvaluateAnonymousSnippet("header_template.jsonnet",
		"(import '../http.libsonnet').res.headerTemplate({ 'X-Method': '{{ .Request.Method }}' })")
	require.NoError(t, err)
	require.JSONEq(t, `{"header_template": {"X-Method": "{{ .Request.Method }}"}}`, j)
}
