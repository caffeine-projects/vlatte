package openapi

import (
	json "encoding/json/v2"
	"encoding/xml"
	"maps"
	"slices"
	"strings"
)

// encodeBody returns v, a value of the spec or a fake one, as the body of a mock file in mediaType, as its body key
// takes it: an object or a list in JSON as is, anything else as text. schema, the schema of the body, names the root
// element of XML.
func encodeBody(mediaType string, v any, schema any) any {
	mt, _, _ := strings.Cut(strings.ToLower(mediaType), ";")
	mt = strings.TrimSpace(mt)

	switch {
	case isJSON(mt):
		switch v.(type) {
		case map[string]any, []any:
			return v
		}

		b, _ := json.Marshal(v, json.Deterministic(true))

		return string(b)
	case isXML(mt):
		if s, ok := v.(string); ok {
			return s
		}

		var b strings.Builder

		b.WriteString(xml.Header)

		// A document has one root: a list is wrapped in it.
		name := xmlName(schema, "root")
		if list, ok := v.([]any); ok {
			var items any
			if s, ok := schema.(map[string]any); ok {
				items = s["items"]
			}

			b.WriteString("<" + name + ">")
			writeXML(&b, xmlName(items, "item"), list)
			b.WriteString("</" + name + ">")
		} else {
			writeXML(&b, name, v)
		}

		return b.String()
	default:
		return text(v)
	}
}

func isJSON(mt string) bool {
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func isXML(mt string) bool {
	return mt == "application/xml" || mt == "text/xml" || strings.HasSuffix(mt, "+xml")
}

// xmlName returns the element name of schema: its xml.name, the name of the schema its $ref points to, or fallback.
func xmlName(schema any, fallback string) string {
	s, ok := schema.(map[string]any)
	if !ok {
		return fallback
	}

	if x, ok := s["xml"].(map[string]any); ok {
		if name, ok := x["name"].(string); ok && name != "" {
			return name
		}
	}

	if ref, ok := s["$ref"].(string); ok {
		if _, name, ok := strings.Cut(ref, "#/$defs/"); ok && name != "" {
			return name
		}
	}

	return fallback
}

// writeXML writes v as an element named name: an object's properties as elements, in name order, a list's items as
// elements named name each, as OpenAPI writes a list that isn't wrapped, and anything else as text.
func writeXML(b *strings.Builder, name string, v any) {
	if list, ok := v.([]any); ok {
		for _, item := range list {
			writeXML(b, name, item)
		}

		return
	}

	b.WriteString("<" + name + ">")

	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			writeXML(b, k, x[k])
		}
	case nil:
	default:
		_ = xml.EscapeText(b, []byte(text(x)))
	}

	b.WriteString("</" + name + ">")
}
