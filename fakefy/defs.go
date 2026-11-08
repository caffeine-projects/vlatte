package fakefy

import (
	"maps"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// _defsPrefix starts a $ref to a schema under $defs.
	_defsPrefix = "#/$defs/"

	// _defsURL is where the validator of a [Defs] reads its schemas.
	_defsURL = "defs.json"
)

// Defs holds schemas that the schemas of many values name under $defs, such as the shared schemas of an OpenAPI
// document, for every [Faker] given [WithDefs] to share. A Faker checks each value against its schema, which it
// compiles: with Defs, the schemas of Defs compile once, not once for each value whose schema reaches them, which keeps
// a large document whose schemas reach one another from taking minutes. Defs is safe for concurrent use. It keeps
// schemas, and the caller must not change them while it uses Defs. It keeps what it compiles for each value too, so
// one Defs serves the values of one document, not a long-lived process.
type Defs struct {
	schemas map[string]any

	mu       sync.Mutex
	compiler *jsonschema.Compiler // nil when schemas don't all compile as JSON Schema 2020-12
	values   int                  // the schemas compiled with compiler, each a resource of its own
}

// NewDefs creates a [Defs] of schemas, by name, and compiles them.
func NewDefs(schemas map[string]any) *Defs {
	return &Defs{schemas: schemas, compiler: compile(schemas)}
}

type defsOption struct{ d *Defs }

func (o defsOption) apply(f *Faker) { f.defs = o.d }

// WithDefs has a $ref to #/$defs/name resolve, past the schema's own $defs, to the schema d holds by that name, as
// though the schema's $defs held it: the values are those of the schema with the schemas it reaches added to its
// $defs.
func WithDefs(d *Defs) Option {
	return defsOption{d: d}
}

// root returns schema with the schemas of d its $refs reach, in turn, added to its $defs, but for the names its own
// $defs hold: the document its $refs resolve in.
func (d *Defs) root(schema map[string]any) map[string]any {
	own, _ := schema["$defs"].(map[string]any)
	defs := maps.Clone(own)
	if defs == nil {
		defs = make(map[string]any)
	}

	d.reach(schema, defs)

	if len(defs) == len(own) {
		return schema
	}

	root := maps.Clone(schema)
	root["$defs"] = defs

	return root
}

// reach adds to defs each schema of d that a $ref in v names, and those that one names, in turn, but for the names
// defs holds. It reads every $ref key as one: one in data, such as an example, only adds a schema.
func (d *Defs) reach(v any, defs map[string]any) {
	refs(v, func(name string) {
		if def, shared := d.schemas[name]; shared {
			if _, added := defs[name]; !added {
				defs[name] = def
				d.reach(def, defs)
			}
		}
	})
}

// validator returns the validator of schema, its $refs resolving as [Defs.root] has them, as [validator] does, but
// compiling each schema of d once: schema refers to them in the document of d. A schema whose $refs might resolve
// otherwise there, such as one with a $id, a $schema or a $defs name d holds too, or one that doesn't compile so, is
// compiled with the schemas it reaches, as [validator] does.
func (d *Defs) validator(schema, root map[string]any) *jsonschema.Schema {
	if sch := d.shared(schema); sch != nil {
		return sch
	}

	return validator(root)
}

// shared returns the validator of schema in the compiler of d, or nil when it doesn't compile there as it would
// with the schemas it reaches.
func (d *Defs) shared(schema map[string]any) *jsonschema.Schema {
	if d.compiler == nil {
		return nil
	}

	// A $id or a $schema at any depth moves where its $refs resolve, or how.
	if anyObject(schema, func(o map[string]any) bool { return has(o, "$id") || has(o, "$schema") }) {
		return nil
	}

	own, _ := schema["$defs"].(map[string]any)
	for name := range own {
		if _, ok := d.schemas[name]; ok {
			return nil
		}
	}

	// Each schema of d it names refers to d's.
	stubs := make(map[string]any)
	refs(schema, func(name string) {
		if _, ok := d.schemas[name]; ok {
			stubs[name] = map[string]any{"$ref": _defsURL + _defsPrefix + escapeToken(name)}
		}
	})

	if len(stubs) == 0 {
		return nil
	}

	defs := maps.Clone(own)
	if defs == nil {
		defs = make(map[string]any, len(stubs))
	}

	maps.Copy(defs, stubs)

	doc := maps.Clone(schema)
	doc["$defs"] = defs

	inst, err := asJSON(doc)
	if err != nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// AddResource fails only for a URL added before, and loc is new.
	d.values++
	loc := "value" + strconv.Itoa(d.values) + ".json"
	_ = d.compiler.AddResource(loc, inst)

	sch, err := d.compiler.Compile(loc)
	if err != nil {
		return nil
	}

	return sch
}

// compile returns a compiler that holds schemas, under $defs, as JSON Schema 2020-12 with formats asserted, or nil
// when they don't all compile so, or when a reference of theirs may resolve otherwise there than in the schema of a
// value: a $ref that is not to a schema under $defs, such as # for the root, one under a $id, or a $dynamicRef or
// $dynamicAnchor, which resolve by the resources a value passes through.
func compile(schemas map[string]any) *jsonschema.Compiler {
	moved := anyObject(schemas, func(o map[string]any) bool {
		_, named := defName(o["$ref"])
		return has(o, "$id") || has(o, "$dynamicRef") || has(o, "$dynamicAnchor") || has(o, "$ref") && !named
	})
	if moved {
		return nil
	}

	doc, err := asJSON(map[string]any{"$defs": schemas})
	if err != nil {
		return nil
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	// AddResource fails only for a URL added before: Compile fails then too.
	_ = c.AddResource(_defsURL, doc)

	if _, err := c.Compile(_defsURL); err != nil {
		return nil
	}

	return c
}

// refs calls yield with the name of each schema under $defs that a $ref in v points into.
func refs(v any, yield func(name string)) {
	anyObject(v, func(o map[string]any) bool {
		if name, ok := defName(o["$ref"]); ok {
			yield(name)
		}

		return false
	})
}

// anyObject reports whether found holds for an object of v, at any depth, calling it on each until it does.
func anyObject(v any, found func(o map[string]any) bool) bool {
	switch x := v.(type) {
	case map[string]any:
		if found(x) {
			return true
		}

		for _, val := range x {
			if anyObject(val, found) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if anyObject(item, found) {
				return true
			}
		}
	}

	return false
}

// defName returns the name of the schema under $defs that ref, a fragment such as #/$defs/Pet/properties/name, points
// into, decoded as [walk.pointer] decodes it.
func defName(ref any) (string, bool) {
	s, _ := ref.(string)
	frag, _ := url.PathUnescape(s) // "" for a bad escape, which names no schema

	rest, ok := strings.CutPrefix(frag, _defsPrefix)
	if !ok {
		return "", false
	}

	token, _, _ := strings.Cut(rest, "/")

	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"), true
}

// escapeToken returns name as a token of a JSON pointer in a URI fragment.
func escapeToken(name string) string {
	return url.PathEscape(strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1"))
}
