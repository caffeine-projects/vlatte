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
	// The node holds the order of the keys, which the data model of the document doesn't.
	var node yaml.Node

	doc, err := config.Parse(OpenAPIDocument, _formatYAML)
	if err == nil {
		err = yaml.Unmarshal(OpenAPIDocument, &node)
	}

	if err != nil {
		return nil, err
	}

	var out bytes.Buffer

	enc := orderedEncoder{enc: jsontext.NewEncoder(&out, jsontext.WithIndent("  "))}
	enc.encode(node.Content[0], doc)

	return out.Bytes(), enc.err
})

// orderedEncoder writes a value of the data model as JSON, with the keys of its objects in the order of the YAML node
// it decodes from. It stops at the first error, which it keeps.
type orderedEncoder struct {
	enc *jsontext.Encoder
	err error
}

// encode writes v, the value node decodes to.
func (e *orderedEncoder) encode(node *yaml.Node, v any) {
	switch x := v.(type) {
	case map[string]any:
		e.token(jsontext.BeginObject)

		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			e.token(jsontext.String(key))
			e.encode(node.Content[i+1], x[key])
		}

		e.token(jsontext.EndObject)
	case []any:
		e.token(jsontext.BeginArray)

		for i, item := range x {
			e.encode(node.Content[i], item)
		}

		e.token(jsontext.EndArray)
	default:
		if e.err == nil {
			e.err = json.MarshalEncode(e.enc, v)
		}
	}
}

func (e *orderedEncoder) token(t jsontext.Token) {
	if e.err == nil {
		e.err = e.enc.WriteToken(t)
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
