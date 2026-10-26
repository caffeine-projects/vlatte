package handler

import (
	"bytes"
	_ "embed"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// OpenAPIDocument is the OpenAPI document of the admin API, in YAML.
//
//go:embed openapi.yaml
var OpenAPIDocument []byte

// _openAPIJSON is the OpenAPI document in JSON, with the keys of its objects in the order the YAML document has them:
// renderers, such as the docs page, list its paths and operations in that order, which keeps related ones together.
var _openAPIJSON = sync.OnceValues(func() ([]byte, error) {
	doc, err := config.Parse(OpenAPIDocument, _formatYAML)
	if err != nil {
		return nil, err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(OpenAPIDocument, &node); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := encodeInOrder(jsontext.NewEncoder(&out, jsontext.WithIndent("  ")), node.Content[0], doc); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
})

// encodeInOrder writes v, the value of the data model that node decodes to, as JSON, with the keys of its objects in
// the order node has them.
func encodeInOrder(enc *jsontext.Encoder, node *yaml.Node, v any) error {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	switch x := v.(type) {
	case map[string]any:
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}

		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if err := enc.WriteToken(jsontext.String(key)); err != nil {
				return err
			}

			if err := encodeInOrder(enc, node.Content[i+1], x[key]); err != nil {
				return err
			}
		}

		return enc.WriteToken(jsontext.EndObject)
	case []any:
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}

		for i, e := range x {
			if err := encodeInOrder(enc, node.Content[i], e); err != nil {
				return err
			}
		}

		return enc.WriteToken(jsontext.EndArray)
	default:
		return json.MarshalEncode(enc, v)
	}
}

// OpenAPI answers the OpenAPI document of the admin API, in JSON or YAML: GET /openapi.
func OpenAPI() http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		f, _ := responseFormat(r)
		if f.format == _formatYAML {
			write(w, http.StatusOK, f.contentType, OpenAPIDocument)
			return
		}

		body, err := _openAPIJSON()
		if err != nil {
			fail(w, r, err)
			return
		}

		write(w, http.StatusOK, f.contentType, body)
	})
}
