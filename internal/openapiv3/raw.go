package openapiv3

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// raw holds the example fields of the spec's objects as the spec writes them, which kin-openapi changes as it reads
// them: it reads every number as a float64, which rounds an integer past 2^53, and a null as no value. A field is
// keyed by the object kin-openapi read it into, such as a *openapi3.MediaType, and its name, such as example. A field
// the spec doesn't write isn't there. A schema is there whole, under the name "".
type raw map[field]any

// field names a field of an object of kin-openapi.
type field struct {
	object any
	name   string
}

// value returns field name of object as the spec writes it, or kin, what kin-openapi read, when the spec's text isn't
// at hand. It reports whether the field holds a value, null included.
func (r raw) value(object any, name string, kin any) (any, bool) {
	if v, ok := r[field{object: object, name: name}]; ok {
		return v, true
	}

	return kin, kin != nil
}

// node is a value of a file of the spec.
type node struct {
	file *url.URL
	v    any
}

// rawReader reads the files of a spec, which kin-openapi read, as they are written.
type rawReader struct {
	data  map[string][]byte // the files, by fileKey
	files map[string]any    // the files decoded, by fileKey
	out   raw
}

// readRaw returns the example fields of the objects of t, which kin-openapi read from files, by fileKey: the spec at
// location and the files its references name. A field it cannot find, as kin-openapi did, is left out, for what
// kin-openapi read to stand.
func readRaw(t *openapi3.T, location *url.URL, files map[string][]byte) raw {
	r := &rawReader{data: files, files: make(map[string]any), out: make(raw)}
	doc := node{file: location, v: r.file(location)}

	if t.Paths != nil {
		paths := r.child(doc, "paths")
		for name, item := range t.Paths.Map() {
			r.pathItem(item, r.child(paths, name))
		}
	}

	webhooks := r.child(doc, "webhooks")
	for name, item := range t.Webhooks {
		r.pathItem(item, r.child(webhooks, name))
	}

	if c := t.Components; c != nil {
		n := r.child(doc, "components")

		schemas := r.child(n, "schemas")
		for name, ref := range c.Schemas {
			r.schema(ref, r.child(schemas, name))
		}

		r.parameters(c.Parameters, r.child(n, "parameters"))
		r.examples(c.Examples, r.child(n, "examples"))
		r.callbacks(c.Callbacks, r.child(n, "callbacks"))

		for name, ref := range c.RequestBodies {
			if ref != nil && ref.Value != nil {
				r.content(ref.Value.Content, r.child(r.child(r.child(n, "requestBodies"), name), "content"))
			}
		}

		for name, ref := range c.Responses {
			r.response(ref.Value, r.child(r.child(n, "responses"), name))
		}
	}

	return r.out
}

// fileKey returns what names the file of u: u without its fragment.
func fileKey(u *url.URL) string {
	f := *u
	f.Fragment, f.RawFragment = "", ""

	return f.String()
}

// file returns the file at u decoded, as JSON or else YAML, as kin-openapi reads it, or nil when it has none.
func (r *rawReader) file(u *url.URL) any {
	key := fileKey(u)
	if v, ok := r.files[key]; ok {
		return v
	}

	data := r.data[key]

	v, err := config.Parse(data, "json")
	if err != nil {
		v, _ = config.Parse(data, "yaml")
	}

	r.files[key] = v

	return v
}

// deref returns n, or the value its $ref names, as many times as one names another. A reference it cannot follow
// gives no value.
func (r *rawReader) deref(n node) node {
	for range 32 {
		m, _ := n.v.(map[string]any)

		ref, ok := m["$ref"].(string)
		if !ok {
			return n
		}

		u, err := url.Parse(ref)
		if err != nil {
			return node{}
		}

		target := n.file.ResolveReference(u)
		n = node{file: target, v: pointer(r.file(target), target.Fragment)}
	}

	return node{}
}

// pointer returns the value of doc that ptr, a JSON pointer, such as /components/examples/A, names, or nil.
func pointer(doc any, ptr string) any {
	if ptr == "" {
		return doc
	}

	v := doc
	for token := range strings.SplitSeq(strings.TrimPrefix(ptr, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")

		var next any

		switch x := v.(type) {
		case map[string]any:
			next = x[token]
		case []any:
			if i, err := strconv.Atoi(token); err == nil && i >= 0 && i < len(x) {
				next = x[i]
			}
		}

		v = next
	}

	return v
}

// child returns the field name of the object n is, or refers to.
func (r *rawReader) child(n node, name string) node {
	n = r.deref(n)
	m, _ := n.v.(map[string]any)

	return node{file: n.file, v: m[name]}
}

// record keeps field name of the object n is, or refers to, as the field of object, what kin-openapi read it into,
// when n writes it.
func (r *rawReader) record(object any, n node, name string) {
	n = r.deref(n)
	m, _ := n.v.(map[string]any)

	if v, ok := m[name]; ok {
		r.out[field{object: object, name: name}] = v
	}
}

// pathItem records the example fields of p, which kin-openapi read from n, and of the objects within it, as the
// walks after it do for theirs. kin-openapi resolves every reference it loads, or fails: a reference holds its value.
func (r *rawReader) pathItem(p *openapi3.PathItem, n node) {
	if p == nil {
		return
	}

	r.parameterList(p.Parameters, r.child(n, "parameters"))

	for method, op := range p.Operations() {
		on := r.child(n, strings.ToLower(method))
		if on.v == nil {
			on = r.child(r.child(n, "additionalOperations"), method)
		}

		r.operation(op, on)
	}
}

func (r *rawReader) operation(op *openapi3.Operation, n node) {
	r.parameterList(op.Parameters, r.child(n, "parameters"))

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		r.content(op.RequestBody.Value.Content, r.child(r.child(n, "requestBody"), "content"))
	}

	if op.Responses != nil {
		responses := r.child(n, "responses")
		for status, ref := range op.Responses.Map() {
			r.response(ref.Value, r.child(responses, status))
		}
	}
}

func (r *rawReader) callbacks(cs openapi3.Callbacks, n node) {
	for name, ref := range cs {
		cn := r.child(n, name)
		for expr, item := range ref.Value.Map() {
			r.pathItem(item, r.child(cn, expr))
		}
	}
}

// parameterList walks ps, which kin-openapi read in the order of the list n.
func (r *rawReader) parameterList(ps openapi3.Parameters, n node) {
	list := r.items(n)

	for i, ref := range ps {
		if i < len(list) {
			r.parameter(ref.Value, list[i])
		}
	}
}

// items returns the items of the list n is, or refers to.
func (r *rawReader) items(n node) []node {
	n = r.deref(n)
	list, _ := n.v.([]any)

	out := make([]node, len(list))
	for i, v := range list {
		out[i] = node{file: n.file, v: v}
	}

	return out
}

func (r *rawReader) parameters(ps openapi3.ParametersMap, n node) {
	for name, ref := range ps {
		r.parameter(ref.Value, r.child(n, name))
	}
}

func (r *rawReader) parameter(p *openapi3.Parameter, n node) {
	r.record(p, n, "example")
	r.examples(p.Examples, r.child(n, "examples"))
	r.content(p.Content, r.child(n, "content"))

	if p.Schema != nil && p.Schema.Value != nil {
		sn := r.child(n, "schema")
		r.record(p.Schema.Value, sn, "example")
		r.record(p.Schema.Value, sn, "examples")
		r.schema(p.Schema, sn)
	}
}

func (r *rawReader) headers(hs openapi3.Headers, n node) {
	for name, ref := range hs {
		hn := r.child(n, name)
		r.schema(ref.Value.Schema, r.child(hn, "schema"))
		r.record(ref.Value, hn, "example")
		r.examples(ref.Value.Examples, r.child(hn, "examples"))
		r.content(ref.Value.Content, r.child(hn, "content"))
	}
}

func (r *rawReader) response(res *openapi3.Response, n node) {
	r.headers(res.Headers, r.child(n, "headers"))
	r.content(res.Content, r.child(n, "content"))
}

func (r *rawReader) content(c openapi3.Content, n node) {
	for name, mt := range c {
		if mt == nil {
			continue
		}

		mn := r.child(n, name)
		r.schema(mt.Schema, r.child(mn, "schema"))
		r.record(mt, mn, "example")
		r.examples(mt.Examples, r.child(mn, "examples"))
	}
}

func (r *rawReader) examples(es openapi3.Examples, n node) {
	for name, ref := range es {
		r.record(ref.Value, r.child(n, name), "value")
	}
}

// schema records the schema of ref, which kin-openapi read from n, whole, and each schema within it.
func (r *rawReader) schema(ref *openapi3.SchemaRef, n node) {
	if ref == nil || ref.Value == nil {
		return
	}

	s, key := ref.Value, field{object: ref.Value}
	if _, ok := r.out[key]; ok {
		return
	}

	n = r.deref(n)
	r.out[key] = n.v

	for name, sub := range map[string]*openapi3.SchemaRef{
		"items": s.Items, "not": s.Not, "contains": s.Contains, "propertyNames": s.PropertyNames, "contentSchema": s.ContentSchema,
		"if": s.If, "then": s.Then, "else": s.Else, "additionalProperties": s.AdditionalProperties.Schema,
		"unevaluatedItems": s.UnevaluatedItems.Schema, "unevaluatedProperties": s.UnevaluatedProperties.Schema,
	} {
		r.schema(sub, r.child(n, name))
	}

	for name, list := range map[string]openapi3.SchemaRefs{"allOf": s.AllOf, "anyOf": s.AnyOf, "oneOf": s.OneOf, "prefixItems": s.PrefixItems} {
		items := r.items(r.child(n, name))
		for i, sub := range list {
			if i < len(items) {
				r.schema(sub, items[i])
			}
		}
	}

	for name, named := range map[string]openapi3.Schemas{"properties": s.Properties, "patternProperties": s.PatternProperties, "dependentSchemas": s.DependentSchemas, "$defs": s.Defs} {
		mn := r.child(n, name)
		for key, sub := range named {
			r.schema(sub, r.child(mn, key))
		}
	}
}

// exact returns kin, a value of the spec as kin-openapi read it, with each integer as raw, the same value as the spec
// writes it, has it, where kin-openapi rounded it.
func exact(kin, raw any) any {
	switch k := kin.(type) {
	case map[string]any:
		r, _ := raw.(map[string]any)
		for key, v := range k {
			k[key] = exact(v, r[key])
		}
	case []any:
		r, _ := raw.([]any)
		for i := range min(len(k), len(r)) {
			k[i] = exact(k[i], r[i])
		}
	case int64:
		if r, ok := raw.(int64); ok && float64(r) == float64(k) {
			return r
		}
	}

	return kin
}
