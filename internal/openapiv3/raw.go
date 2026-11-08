package openapiv3

import (
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/openapi"
)

// raw holds the example fields of the spec's objects as the spec writes them, which kin-openapi changes as it reads
// them: it reads every number as a float64, which rounds an integer past 2^53, and a null as no value. A field is
// keyed by the object kin-openapi read it into, such as a *openapi3.MediaType, and its name, such as example. A field
// the spec doesn't write isn't there. A schema is there whole, under the name "": a *openapi3.Schema as its $refs lead
// to it, and a *openapi3.SchemaRef of a $ref as written, with the keywords beside it.
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

// places says where the fields of a spec are, and what the spec writes for the scalars of their values: their text,
// such as 1.0 or 01234, which reading them as values loses.
type places struct {
	location *url.URL              // the spec's
	data     map[string][]byte     // the files of the spec, by fileKey
	objects  map[field]node        // where the object of each field looked for is, but its value
	nodes    map[string]*yaml.Node // the files parsed, by fileKey, as text needs them
}

// of returns where the object of field f is in the spec, such as spec.yaml#/paths/~1pets, or "" when the spec
// doesn't give it.
func (p *places) of(f field) string {
	n, ok := p.objects[f]
	if !ok {
		return ""
	}

	return relative(p.location, n.file) + "#" + n.ptr
}

// text returns the text the spec writes for the scalar at ptr, a JSON pointer, within the value of field f, such as
// 01234 for the integer 668, and whether the spec writes one there.
func (p *places) text(f field, ptr string) (string, bool) {
	n, ok := p.objects[f]
	if !ok {
		return "", false
	}

	key := fileKey(n.file)

	doc, ok := p.nodes[key]
	if !ok {
		// JSON is YAML too, which keeps the text of each scalar. kin-openapi read the file: a file it cannot read
		// again leaves doc empty, where scalarAt finds nothing.
		doc = new(yaml.Node)
		yaml.Unmarshal(p.data[key], doc)

		p.nodes[key] = doc
	}

	y := scalarAt(doc, n.ptr+"/"+pointerToken(f.name)+ptr)
	if y == nil {
		return "", false
	}

	return y.Value, true
}

// scalarAt returns the scalar of doc that ptr, a JSON pointer, names, or nil.
func scalarAt(doc *yaml.Node, ptr string) *yaml.Node {
	y := resolveYAML(doc)

	for token := range strings.SplitSeq(strings.TrimPrefix(ptr, "/"), "/") {
		if y == nil {
			return nil
		}

		token = unescapeToken(token)

		var next *yaml.Node

		switch y.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(y.Content); i += 2 {
				if y.Content[i].Value == token {
					next = y.Content[i+1]
				}
			}
		case yaml.SequenceNode:
			if i, err := strconv.Atoi(token); err == nil && i >= 0 && i < len(y.Content) {
				next = y.Content[i]
			}
		}

		y = resolveYAML(next)
	}

	if y == nil || y.Kind != yaml.ScalarNode {
		return nil
	}

	return y
}

// resolveYAML returns the value y holds: the content of a document, what an alias names, or y.
func resolveYAML(y *yaml.Node) *yaml.Node {
	for y != nil {
		switch {
		case y.Kind == yaml.DocumentNode && len(y.Content) > 0:
			y = y.Content[0]
		case y.Kind == yaml.AliasNode:
			y = y.Alias
		default:
			return y
		}
	}

	return nil
}

// node is a value of a file of the spec, at a JSON pointer.
type node struct {
	file *url.URL
	ptr  string
	v    any
}

// rawReader reads the files of a spec, which kin-openapi read, as they are written.
type rawReader struct {
	location *url.URL          // the spec's
	v31      bool              // the spec is OpenAPI 3.1 or later, where an annotation beside a $ref replaces its schema's
	data     map[string][]byte // the files, by fileKey
	files    map[string]any    // the files decoded, by fileKey
	out      raw
	places   *places
	servers  map[*openapi3.Server]*url.URL // the file each server of a path or an operation is written in
}

// readRaw returns the example fields of the objects of t, which kin-openapi read from files, by fileKey: the spec at
// location and the files its references name. A field it cannot find, as kin-openapi did, is left out, for what
// kin-openapi read to stand. It returns as well the places of the fields it looked for, written or not, and the file
// each server of a path or an operation is written in, which a relative URL of it is relative to. A field of a $ref
// beside it, such as its example, is that of the *openapi3.SchemaRef.
func readRaw(t *openapi3.T, location *url.URL, files map[string][]byte) (raw, *places, map[*openapi3.Server]*url.URL) {
	r := &rawReader{
		location: location, v31: t.IsOpenAPI31OrLater(), data: files, files: make(map[string]any), out: make(raw),
		places:  &places{location: location, data: files, objects: make(map[field]node), nodes: make(map[string]*yaml.Node)},
		servers: make(map[*openapi3.Server]*url.URL),
	}
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

	return r.out, r.places, r.servers
}

// fileKey returns what names the file of u: u without its fragment, and a file's path with a leading slash, which a
// reference resolved against a Windows path, such as C:/api/spec.yaml, gains.
func fileKey(u *url.URL) string {
	f := *u
	f.Fragment, f.RawFragment = "", ""

	if f.Host == "" && !strings.HasPrefix(f.Path, "/") {
		f.Path = "/" + f.Path
	}

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
// gives no value, as does one that leads back to a value it named before.
func (r *rawReader) deref(n node) node {
	nodes := r.chain(n)

	return nodes[len(nodes)-1]
}

// chain returns n and the values its $refs lead to, each naming the next, up to the first without a $ref. A
// reference it cannot follow ends it with no value, as does one that leads back to a value of the chain: a chain of
// any length ends.
func (r *rawReader) chain(n node) []node {
	nodes := []node{n}
	seen := make(map[string]bool)

	for {
		next, ok := r.next(n)
		if !ok {
			return nodes
		}

		seen[n.place()] = true
		if next.file == nil || seen[next.place()] {
			return append(nodes, node{})
		}

		nodes = append(nodes, next)
		n = next
	}
}

// place names where n is in the spec: its file and its JSON pointer.
func (n node) place() string {
	return fileKey(n.file) + "#" + n.ptr
}

// next returns the value the $ref of n names, and whether n has a $ref. A reference that isn't a URL gives no value.
func (r *rawReader) next(n node) (node, bool) {
	m, _ := n.v.(map[string]any)

	ref, ok := m["$ref"].(string)
	if !ok {
		return n, false
	}

	u, err := url.Parse(ref)
	if err != nil {
		return node{}, true
	}

	target := n.file.ResolveReference(u)

	return node{file: target, ptr: target.Fragment, v: pointer(r.file(target), target.Fragment)}, true
}

// pointer returns the value of doc that ptr, a JSON pointer, such as /components/examples/A, names, or nil.
func pointer(doc any, ptr string) any {
	if ptr == "" {
		return doc
	}

	v := doc
	for token := range strings.SplitSeq(strings.TrimPrefix(ptr, "/"), "/") {
		token = unescapeToken(token)

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
	return member(r.deref(n), name)
}

// member returns the field name of object n.
func member(n node, name string) node {
	m, _ := n.v.(map[string]any)

	return node{file: n.file, ptr: n.ptr + "/" + pointerToken(name), v: m[name]}
}

// pointerToken returns name as a token of a JSON pointer: its ~ and / escaped.
func pointerToken(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

// unescapeToken returns the name token, of a JSON pointer, names: pointerToken undone.
func unescapeToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

// itemField returns the field name of path item n, as kin-openapi reads it: it puts each field written beside the
// $ref of a path item, but a null or an empty list, over the item's. So it is the field of the first item of the chain
// n starts that writes it.
func (r *rawReader) itemField(n node, name string) node {
	nodes := r.chain(n)

	i := slices.IndexFunc(nodes, func(item node) bool {
		m, _ := item.v.(map[string]any)
		list, isList := m[name].([]any)

		return m[name] != nil && (!isList || len(list) > 0)
	})
	if i < 0 {
		i = len(nodes) - 1
	}

	return member(nodes[i], name)
}

// record keeps field name of the object n is, or refers to, as the field of object, what kin-openapi read it into,
// when n writes it.
func (r *rawReader) record(object any, n node, name string) {
	r.keep(object, r.deref(n), name)
}

// keep keeps field name of object n, as the field of object, what kin-openapi read it into, when n writes it, and
// where it is, as locate does.
func (r *rawReader) keep(object any, n node, name string) {
	r.locate(object, n, name)

	m, _ := n.v.(map[string]any)
	if v, ok := m[name]; ok {
		r.out[field{object: object, name: name}] = v
	}
}

// locate keeps where n, which field name of object is of, is, when it has a place: a reference it cannot follow gives
// none.
func (r *rawReader) locate(object any, n node, name string) {
	if n.file != nil {
		r.places.objects[field{object: object, name: name}] = node{file: n.file, ptr: n.ptr}
	}
}

// _refSiblingAnnotations are the keywords that, beside a $ref, replace those of the schema it refers to, as
// kin-openapi applies them: they describe a value rather than constrain it.
var _refSiblingAnnotations = []string{
	"$comment", "allowEmptyValue", "default", "deprecated", "description", "discriminator", "example", "examples",
	"externalDocs", "nullable", "readOnly", "title", "writeOnly", "xml",
}

// siblings returns the keywords beside the $ref of schema n that kin-openapi applies, in OpenAPI 3.1 only: the
// annotations, which replace those of the schema it refers to, and the assertions, which it adds as one more allOf
// entry.
func (r *rawReader) siblings(n node) (annotations, assertions map[string]any) {
	m, _ := n.v.(map[string]any)
	if _, ok := m["$ref"]; !ok || !r.v31 {
		return nil, nil
	}

	annotations, assertions = make(map[string]any), make(map[string]any)

	for k, v := range m {
		switch {
		case k == "$ref" || strings.HasPrefix(k, "x-"):
		case slices.Contains(_refSiblingAnnotations, k):
			annotations[k] = v
		default:
			assertions[k] = v
		}
	}

	return annotations, assertions
}

// resolved returns schema n as kin-openapi reads it: the schema its $refs lead to, as chain has it, with the keywords
// beside each $ref applied, as siblings has them, the last $ref's first.
func (r *rawReader) resolved(n node) any {
	nodes := r.chain(n)
	v := nodes[len(nodes)-1].v

	for _, ref := range slices.Backward(nodes[:len(nodes)-1]) {
		annotations, assertions := r.siblings(ref)
		if len(annotations) == 0 && len(assertions) == 0 {
			continue
		}

		target, _ := v.(map[string]any)

		out := maps.Clone(target)
		if out == nil {
			out = make(map[string]any)
		}

		maps.Copy(out, annotations)

		if len(assertions) > 0 {
			all, _ := target["allOf"].([]any)
			out["allOf"] = append(slices.Clone(all), assertions)
		}

		v = out
	}

	return v
}

// annotated returns the schema of n, or of the $refs it leads to, whose annotation name kin-openapi read: in OpenAPI
// 3.1, the first that writes it, as one beside a $ref replaces the referenced schema's; in 3.0, the last.
func (r *rawReader) annotated(n node, name string) node {
	nodes := r.chain(n)
	if !r.v31 {
		return nodes[len(nodes)-1]
	}

	for _, s := range nodes {
		m, _ := s.v.(map[string]any)
		if _, ok := m[name]; ok {
			return s
		}
	}

	return nodes[len(nodes)-1]
}

// pathItem records the example fields of p, which kin-openapi read from n, and of the objects within it, as the
// walks after it do for theirs. kin-openapi resolves every reference it loads, or fails: a reference holds its value.
func (r *rawReader) pathItem(p *openapi3.PathItem, n node) {
	if p == nil {
		return
	}

	r.parameterList(p.Parameters, r.itemField(n, "parameters"))
	r.server(p.Servers, r.itemField(n, "servers"))

	for method, op := range p.Operations() {
		on := r.itemField(n, strings.ToLower(method))
		if on.v == nil {
			on = r.child(r.itemField(n, "additionalOperations"), method)
		}

		r.operation(op, on)
	}
}

func (r *rawReader) operation(op *openapi3.Operation, n node) {
	r.parameterList(op.Parameters, r.child(n, "parameters"))

	if op.Servers != nil {
		r.server(*op.Servers, r.child(n, "servers"))
	}

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

// server records the file each of servers, which kin-openapi read from the list n, is written in.
func (r *rawReader) server(servers openapi3.Servers, n node) {
	for _, s := range servers {
		r.servers[s] = n.file
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
		out[i] = node{file: n.file, ptr: n.ptr + "/" + strconv.Itoa(i), v: v}
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

	r.schema(p.Schema, r.child(n, "schema"))
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

// schema records the schema of ref, which kin-openapi read from n, whole, its examples and default, and each schema
// within it. A $ref is recorded as written, under ref, for the keywords beside it, and what it refers to under its
// value.
func (r *rawReader) schema(ref *openapi3.SchemaRef, n node) {
	if ref == nil || ref.Value == nil {
		return
	}

	if ref.Ref != "" {
		r.out[field{object: ref}] = n.v

		for _, name := range []string{"example", "examples"} {
			r.locate(ref, n, name)
		}
	}

	s, key := ref.Value, field{object: ref.Value}
	if _, ok := r.out[key]; ok {
		return
	}

	for _, name := range []string{"default", "example", "examples"} {
		r.keep(s, r.annotated(n, name), name)
	}

	resolved := r.resolved(n)
	r.out[key] = resolved

	// The keywords beside a $ref that assert are one more allOf entry, kin-openapi's last, unless they are all null.
	// A boolean schema has none.
	m, _ := resolved.(map[string]any)
	all, _ := m["allOf"].([]any)
	if _, assertions := r.siblings(n); len(assertions) > 0 && len(s.AllOf) == len(all) {
		r.schema(s.AllOf[len(s.AllOf)-1], node{file: n.file, ptr: n.ptr, v: assertions})
	}

	n = r.deref(n)
	one, lists, named := schemasIn(s)

	for name, sub := range one {
		r.schema(sub, r.child(n, name))
	}

	for name, list := range lists {
		items := r.items(r.child(n, name))
		for i, sub := range list {
			if i < len(items) {
				r.schema(sub, items[i])
			}
		}
	}

	for name, schemas := range named {
		mn := r.child(n, name)
		for key, sub := range schemas {
			r.schema(sub, r.child(mn, key))
		}
	}
}

// schemasIn returns the schemas within s, by the keyword that holds them: one, a list, or a map of them by name.
func schemasIn(s *openapi3.Schema) (one map[string]*openapi3.SchemaRef, lists map[string]openapi3.SchemaRefs, named map[string]openapi3.Schemas) {
	one = map[string]*openapi3.SchemaRef{
		"items": s.Items, "not": s.Not, "contains": s.Contains, "propertyNames": s.PropertyNames, "contentSchema": s.ContentSchema,
		"if": s.If, "then": s.Then, "else": s.Else, "additionalProperties": s.AdditionalProperties.Schema,
		"unevaluatedItems": s.UnevaluatedItems.Schema, "unevaluatedProperties": s.UnevaluatedProperties.Schema,
	}
	lists = map[string]openapi3.SchemaRefs{"allOf": s.AllOf, "anyOf": s.AnyOf, "oneOf": s.OneOf, "prefixItems": s.PrefixItems}
	named = map[string]openapi3.Schemas{"properties": s.Properties, "patternProperties": s.PatternProperties, "dependentSchemas": s.DependentSchemas, "$defs": s.Defs}

	return one, lists, named
}

// exact returns kin, a value of the spec as kin-openapi read it, with each integer as raw, the same value as the spec
// writes it, has it, where kin-openapi rounded it, and each null of a keyword holding a value, such as const, which
// kin-openapi left out, with the allOf entry that holds it.
func exact(kin, raw any) any {
	switch k := kin.(type) {
	case map[string]any:
		r, _ := raw.(map[string]any)
		for key, v := range k {
			k[key] = exact(v, r[key])
		}

		for _, key := range openapi.ValueKeywords {
			if v, ok := r[key]; ok && v == nil {
				k[key] = nil
			}
		}

		// The keywords beside a $ref that assert are one more allOf entry, which kin-openapi leaves out when they
		// are all null, such as const.
		all, _ := k["allOf"].([]any)
		if more, _ := r["allOf"].([]any); len(more) > len(all) {
			k["allOf"] = append(all, more[len(all):]...)
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
	case float64:
		// Rounded past the largest int64, an integer is a float64.
		if r, ok := raw.(int64); ok && float64(r) == k {
			return r
		}
	}

	return kin
}
