// Package openapiv3 reads OpenAPI 3.0, 3.1 and 3.2 documents into an [openapi.Document], with kin-openapi.
package openapiv3

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// _componentsPrefix starts a reference to a shared schema, which the model writes under $defs.
const _componentsPrefix = "#/components/schemas/"

// _valueKeys are the keywords of a schema that hold values, not schemas: their content is never translated.
var _valueKeys = []string{"const", "default", "enum", "example", "examples"}

// _schemaMapKeys are the keywords of a schema that hold schemas by name.
var _schemaMapKeys = []string{"properties", "patternProperties", "$defs", "dependentSchemas"}

// Load reads data, the OpenAPI 3 document at location, and the files its references name, which read returns. It
// fails with [openapi.ErrInvalid] for a document kin-openapi finds invalid.
func Load(ctx context.Context, data []byte, location *url.URL, read func(u *url.URL) ([]byte, error)) (*openapi.Document, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) { return read(u) }

	t, err := loader.LoadFromDataWithPath(data, location)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", openapi.ErrInvalid, err)
	}

	if err := t.Validate(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", openapi.ErrInvalid, err)
	}

	// The model refers to shared schemas by name: a reference to another file becomes one.
	t.InternalizeRefs(ctx, nil)

	c := converter{v30: t.IsOpenAPI30()}
	doc := &openapi.Document{Defs: make(map[string]any)}

	if t.Info != nil {
		doc.Title = t.Info.Title
	}

	root, warnings := basePath(t.Servers, "")
	doc.Warnings = append(doc.Warnings, warnings...)

	if t.Components != nil {
		for name, ref := range t.Components.Schemas {
			doc.Defs[name] = c.schema(ref)
		}
	}

	if t.Paths == nil {
		return doc, nil
	}

	// Sorted, so the warnings come in the same order on every run.
	paths := t.Paths.Map()
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		item := paths[path]

		// A path's servers replace the document's, and an operation's replace its path's.
		itemBase := root
		if len(item.Servers) > 0 {
			itemBase, warnings = basePath(item.Servers, path)
			doc.Warnings = append(doc.Warnings, warnings...)
		}

		ops := item.Operations()
		for _, method := range slices.Sorted(maps.Keys(ops)) {
			op := ops[method]
			o := c.operation(path, strings.ToUpper(method), item.Parameters, op)
			o.BasePath = itemBase

			if op.Servers != nil && len(*op.Servers) > 0 {
				o.BasePath, warnings = basePath(*op.Servers, o.Method+" "+path)
				doc.Warnings = append(doc.Warnings, warnings...)
			}

			doc.Operations = append(doc.Operations, o)
		}
	}

	return doc, nil
}

// basePath returns the path of the first of servers, which the operations they serve are under, and a warning when
// the servers' paths differ: the mocks answer the first one's only. where names what the servers serve in the
// warnings, or is "" for the whole document.
func basePath(servers openapi3.Servers, where string) (string, []string) {
	var (
		paths    []string
		warnings []string
	)

	for _, s := range servers {
		p, err := s.BasePath()
		if err != nil {
			warnings = append(warnings, prefixed(where, fmt.Sprintf("server %s: %v: ignored", s.URL, err)))
			continue
		}

		paths = append(paths, strings.TrimSuffix(p, "/"))
	}

	if len(paths) == 0 {
		return "", warnings
	}

	if slices.ContainsFunc(paths, func(p string) bool { return p != paths[0] }) {
		warnings = append(warnings, prefixed(where, fmt.Sprintf("the servers have different paths: the mocks answer under the first one's, %q", cmp.Or(paths[0], "/"))))
	}

	return paths[0], warnings
}

// prefixed returns warning, after where when where is not "".
func prefixed(where, warning string) string {
	if where == "" {
		return warning
	}

	return where + ": " + warning
}

// converter translates the parts of a document into the model.
type converter struct {
	v30 bool // the document is OpenAPI 3.0, whose schemas are not JSON Schema 2020-12
}

// operation translates op, of the path item with the parameters common.
func (c converter) operation(path, method string, common openapi3.Parameters, op *openapi3.Operation) openapi.Operation {
	o := openapi.Operation{Method: method, Path: path, Summary: op.Summary}

	// An operation's parameter replaces the path item's of the same name and location.
	params := make(map[string]*openapi3.Parameter)
	for _, p := range slices.Concat(common, op.Parameters) {
		if p != nil && p.Value != nil && p.Value.In == openapi3.ParameterInPath {
			params[p.Value.Name] = p.Value
		}
	}

	for _, name := range slices.Sorted(maps.Keys(params)) {
		p := params[name]

		examples := examplesOf(p.Example, p.Examples)
		if p.Schema != nil && p.Schema.Value != nil {
			examples = append(examples, examplesOf(p.Schema.Value.Example, nil)...)
			for _, v := range p.Schema.Value.Examples {
				examples = append(examples, openapi.Example{Value: v})
			}
		}

		o.PathParams = append(o.PathParams, openapi.Parameter{
			Name: name, Style: cmp.Or(p.Style, openapi3.SerializationSimple), Explode: p.Explode != nil && *p.Explode, Examples: examples,
		})
	}

	// Loaded, every response is resolved: Map of no responses is empty.
	responses := op.Responses.Map()
	for _, status := range slices.Sorted(maps.Keys(responses)) {
		o.Responses = append(o.Responses, c.response(status, responses[status].Value))
	}

	return o
}

// response translates the response of status.
func (c converter) response(status string, res *openapi3.Response) openapi.Response {
	r := openapi.Response{Status: strings.ToUpper(status)}
	if status == "default" {
		r.Status = status
	}

	for _, name := range slices.Sorted(maps.Keys(res.Headers)) {
		ref := res.Headers[name]
		if ref == nil || ref.Value == nil || strings.EqualFold(name, "Content-Type") {
			continue
		}

		r.Headers = append(r.Headers, openapi.Header{
			Name: name, Schema: c.schema(ref.Value.Schema), Examples: examplesOf(ref.Value.Example, ref.Value.Examples),
		})
	}

	for _, mt := range slices.Sorted(maps.Keys(res.Content)) {
		media := res.Content[mt]
		r.Content = append(r.Content, openapi.Content{MediaType: mt, Schema: c.schema(media.Schema), Examples: examplesOf(media.Example, media.Examples)})
	}

	return r
}

// examplesOf returns the example value, if any, then the named examples, in name order. A named example that
// isn't inline, but at an externalValue, is left out.
func examplesOf(example any, named openapi3.Examples) []openapi.Example {
	var out []openapi.Example
	if example != nil {
		out = append(out, openapi.Example{Value: example})
	}

	for _, name := range slices.Sorted(maps.Keys(named)) {
		ref := named[name]
		if ref == nil || ref.Value == nil || ref.Value.Value == nil {
			continue
		}

		out = append(out, openapi.Example{Name: name, Value: ref.Value.Value})
	}

	return out
}

// schema translates the schema of ref into JSON Schema 2020-12, or returns nil for no schema. kin-openapi read the
// schema from JSON, which it writes back.
func (c converter) schema(ref *openapi3.SchemaRef) any {
	if ref == nil {
		return nil
	}

	data, _ := json.Marshal(ref)
	v, _ := value.DecodeJSON(data)

	return c.translate(v)
}

// translate rewrites schema v of the document as JSON Schema 2020-12: its references to shared schemas point under
// $defs, and, in OpenAPI 3.0, nullable adds the null type, and a boolean exclusiveMinimum or exclusiveMaximum takes
// the value of minimum or maximum.
func (c converter) translate(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch {
			case slices.Contains(_valueKeys, k):
				out[k] = val
			case slices.Contains(_schemaMapKeys, k):
				out[k] = c.translateEach(val)
			case k == "$ref":
				out[k] = localRef(val)
			case k == "discriminator":
				out[k] = discriminator(val)
			default:
				out[k] = c.translate(val)
			}
		}

		if c.v30 {
			translate30(out)
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = c.translate(item)
		}

		return out
	default:
		return v
	}
}

// translateEach translates each schema of a map of them, such as properties, whose keys are names, not keywords.
func (c converter) translateEach(v any) any {
	m, _ := v.(map[string]any)

	out := make(map[string]any, len(m))
	for k, s := range m {
		out[k] = c.translate(s)
	}

	return out
}

// localRef returns ref pointing under $defs when it points to a shared schema.
func localRef(ref any) any {
	s, _ := ref.(string)
	return strings.Replace(s, _componentsPrefix, "#/$defs/", 1)
}

// discriminator returns the discriminator v with the references of its mapping pointing under $defs.
func discriminator(v any) any {
	d, _ := v.(map[string]any)

	out := make(map[string]any, len(d))
	maps.Copy(out, d)

	if mapping, ok := d["mapping"].(map[string]any); ok {
		m := make(map[string]any, len(mapping))
		for k, ref := range mapping {
			m[k] = localRef(ref)
		}

		out["mapping"] = m
	}

	return out
}

// translate30 rewrites the OpenAPI 3.0 keywords of schema s that JSON Schema 2020-12 writes otherwise.
func translate30(s map[string]any) {
	if nullable, ok := s["nullable"].(bool); ok {
		delete(s, "nullable")

		// OpenAPI 3.0 types are strings, never lists.
		if t, ok := s["type"].(string); ok && nullable {
			s["type"] = []any{t, "null"}
		}
	}

	for _, b := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		excl, ok := s[b[0]].(bool)
		if !ok {
			continue
		}

		delete(s, b[0])

		if bound, ok := s[b[1]]; ok && excl {
			s[b[0]] = bound
			delete(s, b[1])
		}
	}
}
