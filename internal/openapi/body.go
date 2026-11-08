package openapi

import (
	"encoding/base64"
	json "encoding/json/v2"
	"slices"
	"strings"
)

// setBody sets the body of m, of content c, to ex, an example of the spec or a fake value, as a mock file takes it. In
// JSON, an object or a list is the value, which VLatte sends as JSON, and anything else its JSON text. In any other
// media type, the generator writes no format of its own: a string is the body as is, or its bytes when c's schema
// says it is base64 and ex isn't serialized, and a number or a boolean its text. An object or a list there leaves m
// disabled and without a body, with a warning.
func (g *generator) setBody(m *Mock, c Content, ex Example) {
	v := ex.Value
	if IsJSON(c.MediaType) {
		switch v.(type) {
		case map[string]any, []any:
			m.Reply.Body = v
		default:
			b, _ := json.Marshal(v, json.Deterministic(true))
			m.Reply.Body = string(b)
		}

		return
	}

	switch x := v.(type) {
	case map[string]any, []any:
		g.warnf("the body is data, not text: written disabled: give a string example")
		m.Enabled = false
	case string:
		if x != "" && !ex.Serialized && g.isBase64(c.Schema) {
			if _, err := base64.StdEncoding.DecodeString(x); err == nil {
				m.Reply.BodyBase64 = x
				return
			}

			g.warnf("the body isn't base64, as its schema says: written as text")
		}

		m.Reply.Body = x
	default:
		m.Reply.Body = text(v)
	}
}

// IsJSON reports whether mediaType, with or without parameters, is JSON: application/json or a type ending in +json,
// such as application/vnd.api+json.
func IsJSON(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(mediaType), ";")
	mt = strings.TrimSpace(mt)

	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// isBase64 reports whether schema says its string is base64: its contentEncoding is base64, or, as OpenAPI 3.0 writes
// it, its format byte, itself or the schema its $ref points to.
func (g *generator) isBase64(schema any) bool {
	root := g.responses.root(schema)
	defs, _ := root["$defs"].(map[string]any)

	var seen []string

	for s := root; ; {
		if s["contentEncoding"] == "base64" || s["format"] == "byte" {
			return true
		}

		ref, _ := s["$ref"].(string)

		name, ok := strings.CutPrefix(ref, DefsPrefix)
		if !ok || slices.Contains(seen, name) {
			return false
		}

		seen = append(seen, name)
		s, _ = defs[name].(map[string]any)
	}
}
