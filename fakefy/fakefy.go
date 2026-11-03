package fakefy

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"math/rand/v2"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	// ErrUnsatisfiable is wrapped by the error of a schema no value satisfies, such as one whose minimum is above its
	// maximum, a false schema, or one whose required properties nest without end.
	ErrUnsatisfiable = errors.New("fakefy: no value satisfies the schema")

	// ErrUnresolved is wrapped by the error of a $ref that names no schema of the document.
	ErrUnresolved = errors.New("fakefy: unresolved $ref")

	// ErrInvalid is wrapped by the error of a schema that isn't one, such as a number where a schema goes, or a
	// pattern that doesn't compile.
	ErrInvalid = errors.New("fakefy: invalid schema")
)

const (
	// _optionalDepth is how deep a value nests before its objects leave out optional properties and its arrays hold
	// as few items as they may: recursive schemas end there.
	_optionalDepth = 4

	// _maxDepth is how deep a value, or a chain of $refs, nests before the schema counts as one no value ends.
	_maxDepth = 32

	// _maxTries is how many values a unique array item or a pattern gets before the schema counts as unsatisfiable.
	_maxTries = 20

	// _seedSalt is the second seed of the PCG source a seed makes.
	_seedSalt = 0x9e3779b97f4a7c15

	// _maxCount is the largest count, such as maxLength, that counts: no value made reaches it.
	_maxCount = math.MaxInt32
)

// The ends of the ranges of an int64 and of a float64, as fractions: values made stay within them.
var (
	_minInt   = new(big.Rat).SetInt64(math.MinInt64)
	_maxInt   = new(big.Rat).SetInt64(math.MaxInt64)
	_minFloat = new(big.Rat).SetFloat64(-math.MaxFloat64)
	_maxFloat = new(big.Rat).SetFloat64(math.MaxFloat64)
)

// Faker makes fake values. A Faker is not safe for concurrent use.
type Faker struct {
	rnd *rand.Rand
}

// Option configures a [Faker].
type Option interface {
	apply(f *Faker)
}

type seedOption uint64

func (s seedOption) apply(f *Faker) { f.rnd = rand.New(rand.NewPCG(uint64(s), _seedSalt)) }

// WithSeed makes the Faker's values depend on seed alone: two Fakers with one seed make the same values in the same
// order. Without it, each Faker makes values of its own.
func WithSeed(seed uint64) Option {
	return seedOption(seed)
}

// New creates a [Faker].
func New(opts ...Option) *Faker {
	f := &Faker{rnd: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
	for _, opt := range opts {
		opt.apply(f)
	}

	return f
}

// Schema returns a value for schema, a JSON Schema as decoded JSON or YAML: objects are map[string]any, and numbers any
// Go number or a json.Number. The value is decoded JSON too: map[string]any, []any, string, int64 for an integer,
// float64, bool or nil. It shares nothing with schema, so the caller may change it.
//
// A value comes from the first of const, examples, example, default and enum the schema has; then from its format,
// such as email or date-time; then from the name of its field, such as createdAt or price; then from its type and
// bounds. A $ref resolves within schema, allOf merges its schemas, and oneOf and anyOf take one of their branches,
// setting an OpenAPI discriminator's property to name it, unless the branch fixes its value. Of a list of types, the
// first a value satisfies wins, null last, or first from four levels down, so a nullable schema that nests itself
// ends. An optional property or item no value satisfies is left out.
//
// A schema that compiles as JSON Schema 2020-12 checks each value, formats too: one that doesn't validate, such as an
// example the rest of the schema breaks, is made again, from the schema's examples, then without them, up to 20
// times. A schema whose examples leave no value, such as unique items an example repeats, is made without them.
//
// The error wraps [ErrUnsatisfiable], [ErrUnresolved] or [ErrInvalid], naming the place in the value it stopped at.
func (f *Faker) Schema(schema map[string]any) (any, error) {
	return f.Field("", schema)
}

// Field returns a value for schema, as [Faker.Schema] does, for a field called name, such as a property, a query
// parameter or a header: the name picks among the values the schema allows, so an email field gets an email address.
func (f *Faker) Field(name string, schema map[string]any) (any, error) {
	w := walk{f: f, root: schema, examples: true}

	sch := validator(schema)

	var invalid error

	for try := range _maxTries {
		w.examples = w.examples && try < _maxTries/2

		v, err := w.value(schema, name, "", 0)

		// The examples may be what no value satisfies, such as unique items an example repeats.
		if errors.Is(err, ErrUnsatisfiable) && w.examples {
			w.examples = false
			continue
		}

		if err != nil {
			return nil, err
		}

		if sch == nil {
			return v, nil
		}

		if invalid = validate(sch, v); invalid == nil {
			return v, nil
		}
	}

	return nil, fmt.Errorf("%w: %s: no value of %d validates: %w", ErrUnsatisfiable, at(""), _maxTries, invalid)
}

// validator returns the validator of schema as JSON Schema 2020-12, formats asserted, or nil when it doesn't compile as
// one, such as an OpenAPI 3.0 schema whose exclusiveMinimum is a boolean.
func validator(schema map[string]any) *jsonschema.Schema {
	doc, err := asJSON(schema)
	if err != nil {
		return nil
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	// AddResource fails only for a URL added before: Compile fails then too.
	_ = c.AddResource("schema.json", doc)

	sch, err := c.Compile("schema.json")
	if err != nil {
		return nil
	}

	return sch
}

// validate checks v against sch. Every part of v is JSON: a value made, or one of the schema, which is.
func validate(sch *jsonschema.Schema, v any) error {
	inst, _ := asJSON(v)
	return sch.Validate(inst)
}

// asJSON returns v as the validator reads JSON: its numbers as json.Number, its lists as []any.
func asJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

// walk makes the values of one schema document, whose $refs resolve within root. Without examples, values come from
// const and enum, never from examples, example or default.
type walk struct {
	f        *Faker
	root     map[string]any
	examples bool
}

// value returns a value for s, a schema object or boolean, of the field name at path, depth levels down.
func (w *walk) value(s any, name, path string, depth int) (any, error) {
	if depth > _maxDepth {
		return nil, fmt.Errorf("%w: %s: the value nests deeper than %d levels", ErrUnsatisfiable, at(path), _maxDepth)
	}

	m, err := schemaMap(s, path)
	if err != nil {
		return nil, err
	}

	m, err = w.flatten(m, path, 0)
	if err != nil {
		return nil, err
	}

	if v, ok := m["const"]; ok {
		return clone(v), nil
	}

	if ex, ok := m["examples"].([]any); ok && len(ex) > 0 && w.examples {
		return clone(ex[0]), nil
	}

	for _, key := range []string{"example", "default"} {
		if v, ok := m[key]; ok && w.examples {
			return clone(v), nil
		}
	}

	if enum, ok := m["enum"].([]any); ok && len(enum) > 0 {
		return clone(enum[w.f.rnd.IntN(len(enum))]), nil
	}

	if _, ok := m["oneOf"]; ok {
		return w.branch(m, "oneOf", name, path, depth)
	}

	if _, ok := m["anyOf"]; ok {
		return w.branch(m, "anyOf", name, path, depth)
	}

	// A type no value satisfies gives way to the next.
	var v any

	for _, t := range typesOf(m, depth) {
		if v, err = w.typed(t, m, name, path, depth); !errors.Is(err, ErrUnsatisfiable) {
			break
		}
	}

	return v, err
}

// typed returns a value of type t for s, as [walk.value] does.
func (w *walk) typed(t string, s map[string]any, name, path string, depth int) (any, error) {
	switch t {
	case "object":
		return w.object(s, path, depth)
	case "array":
		return w.array(s, name, path, depth)
	case "integer":
		return w.f.integer(s, name, path)
	case "number":
		return w.f.number(s, name, path)
	case "boolean":
		return w.f.rnd.IntN(2) == 1, nil
	case "null":
		return nil, nil
	default:
		return w.f.str(s, name, path)
	}
}

// flatten resolves the $refs of s and merges its allOf schemas into it, depth levels of allOf down.
func (w *walk) flatten(s map[string]any, path string, depth int) (map[string]any, error) {
	if depth > _maxDepth {
		return nil, fmt.Errorf("%w: %s: allOf and $ref nest deeper than %d schemas", ErrUnsatisfiable, at(path), _maxDepth)
	}

	s, err := w.resolve(s, path)
	if err != nil {
		return nil, err
	}

	all, ok := s["allOf"].([]any)
	if !ok {
		return s, nil
	}

	out := maps.Clone(s)
	delete(out, "allOf")

	for _, sub := range all {
		m, err := schemaMap(sub, path)
		if err != nil {
			return nil, err
		}

		if m, err = w.flatten(m, path, depth+1); err != nil {
			return nil, err
		}

		if err := merge(out, m); err != nil {
			return nil, fmt.Errorf("%w: %s: allOf: %w", ErrUnsatisfiable, at(path), err)
		}
	}

	return out, nil
}

// resolve follows the $ref of s, and of the schema it names, until a schema has none. The keywords next to a $ref
// apply with the schema it names, as JSON Schema 2020-12 has it: resolve returns both as an allOf, the keywords first,
// so their annotations, such as an example, win.
func (w *walk) resolve(s map[string]any, path string) (map[string]any, error) {
	for range _maxDepth {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s, nil
		}

		target, err := w.pointer(ref)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %q: %w", ErrUnresolved, at(path), ref, err)
		}

		m, err := schemaMap(target, path)
		if err != nil {
			return nil, err
		}

		if len(s) == 1 {
			s = m
			continue
		}

		rest := maps.Clone(s)
		delete(rest, "$ref")

		return map[string]any{"allOf": []any{rest, m}}, nil
	}

	return nil, fmt.Errorf("%w: %s: $refs chain past %d schemas", ErrUnsatisfiable, at(path), _maxDepth)
}

// pointer returns the part of the root document ref names: ref is a fragment, such as #/$defs/Pet.
func (w *walk) pointer(ref string) (any, error) {
	frag, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil, errors.New("only references within the document resolve")
	}

	frag, err := url.PathUnescape(frag)
	if err != nil {
		return nil, fmt.Errorf("unescaping: %w", err)
	}

	var node any = w.root
	if frag == "" {
		return node, nil
	}

	rest, ok := strings.CutPrefix(frag, "/")
	if !ok {
		return nil, errors.New("expected a JSON pointer after #")
	}

	for tok := range strings.SplitSeq(rest, "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")

		switch n := node.(type) {
		case map[string]any:
			if node, ok = n[tok]; !ok {
				return nil, fmt.Errorf("no member %q", tok)
			}
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n) {
				return nil, fmt.Errorf("no item %q", tok)
			}

			node = n[i]
		default:
			return nil, fmt.Errorf("%q is not in an object or an array", tok)
		}
	}

	return node, nil
}

// branch returns a value for one of the schemas of the oneOf or anyOf of s, picked at random, with the other keywords
// of s applied too. A discriminator, as OpenAPI writes one, has its property name the branch picked.
func (w *walk) branch(s map[string]any, key, name, path string, depth int) (any, error) {
	branches, _ := s[key].([]any)
	if len(branches) == 0 {
		return nil, fmt.Errorf("%w: %s: %s has no schemas", ErrUnsatisfiable, at(path), key)
	}

	rest := maps.Clone(s)
	delete(rest, key)
	delete(rest, "discriminator")

	// A branch no value satisfies, with the rest, gives way to the others, in random order.
	var (
		picked, v any
		both      map[string]any
		err       error
	)

	for _, i := range w.f.rnd.Perm(len(branches)) {
		picked = branches[i]
		both = map[string]any{"allOf": []any{rest, picked}}

		// A branch counts as a level, so a oneOf that names itself ends.
		if v, err = w.value(both, name, path, depth+1); !errors.Is(err, ErrUnsatisfiable) {
			break
		}
	}

	if err != nil {
		return nil, err
	}

	disc, _ := s["discriminator"].(map[string]any)
	prop, _ := disc["propertyName"].(string)
	obj, isObj := v.(map[string]any)

	if prop == "" || !isObj || w.fixes(both, prop, path) {
		return v, nil
	}

	if tag := discriminatorValue(disc, picked); tag != "" {
		obj[prop] = tag
	}

	return obj, nil
}

// fixes reports whether s gives its property prop a const or an enum, or itself one, which fixes every property: a
// discriminator's tag must not replace it.
func (w *walk) fixes(s map[string]any, prop, path string) bool {
	m, err := w.flatten(s, path, 0)
	if err == nil && (has(m, "const") || has(m, "enum")) {
		return true
	}

	props, _ := m["properties"].(map[string]any)

	p, ok := props[prop].(map[string]any)
	if err != nil || !ok {
		return false
	}

	p, err = w.flatten(p, path, 0)

	return err == nil && (has(p, "const") || has(p, "enum"))
}

// discriminatorValue returns the value of a discriminator's property that names picked: its key in the mapping, or
// the last part of its $ref without one.
func discriminatorValue(disc map[string]any, picked any) string {
	m, _ := picked.(map[string]any)
	ref, _ := m["$ref"].(string)

	if ref == "" {
		return ""
	}

	mapping, _ := disc["mapping"].(map[string]any)
	for _, k := range slices.Sorted(maps.Keys(mapping)) {
		if mapping[k] == ref {
			return k
		}
	}

	_, last, _ := strings.CutLast(ref, "/")

	return last
}

// object returns an object with the properties of s. Past _optionalDepth, it leaves out the optional ones minProperties
// doesn't need.
func (w *walk) object(s map[string]any, path string, depth int) (any, error) {
	props, _ := s["properties"].(map[string]any)
	required := make(map[string]bool)

	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}

	extra := s["additionalProperties"]
	if extra == nil || extra == true {
		extra = make(map[string]any)
	}

	out := make(map[string]any)

	// add adds a value of schema, and of the patternProperties key matches, at key. An optional property no value
	// satisfies is left out.
	add := func(key string, schema any) error {
		ps, ok, err := patternSchema(s, key, path)
		if err != nil {
			return err
		}

		if ok {
			schema = bothSchemas(schema, ps)
		}

		v, err := w.value(schema, key, path+"/"+escape(key), depth+1)
		if errors.Is(err, ErrUnsatisfiable) && !required[key] {
			return nil
		}

		if err == nil {
			out[key] = v
		}

		return err
	}

	minProps, _, err := intKeyword(s, "minProperties", path)
	if err != nil {
		return nil, err
	}

	maxProps, boundedProps, err := intKeyword(s, "maxProperties", path)
	if err != nil {
		return nil, err
	}

	if boundedProps && minProps > maxProps {
		return nil, fmt.Errorf("%w: %s: minProperties %d is above maxProperties %d", ErrUnsatisfiable, at(path), minProps, maxProps)
	}

	// Optional properties left out come back for minProperties first, as additionalProperties may refuse others.
	var skipped []string

	for _, key := range slices.Sorted(maps.Keys(props)) {
		if !required[key] && depth >= _optionalDepth {
			skipped = append(skipped, key)
			continue
		}

		if err := add(key, props[key]); err != nil {
			return nil, err
		}
	}

	for _, key := range slices.Sorted(maps.Keys(required)) {
		if has(out, key) {
			continue
		}

		_, ok, err := patternSchema(s, key, path)
		if err != nil {
			return nil, err
		}

		if !ok && extra == false {
			return nil, fmt.Errorf("%w: %s: required property %q is not allowed", ErrUnsatisfiable, at(path), key)
		}

		// add applies the patternProperties key matches; extra applies only to the keys none matches.
		var schema any = true
		if !ok {
			schema = extra
		}

		if err := add(key, schema); err != nil {
			return nil, err
		}
	}

	for _, key := range skipped {
		if len(out) >= minProps {
			break
		}

		if err := add(key, props[key]); err != nil {
			return nil, err
		}
	}

	for i := 1; len(out) < minProps; i++ {
		if extra == false {
			return nil, fmt.Errorf("%w: %s: expected %d properties, not %d", ErrUnsatisfiable, at(path), minProps, len(out))
		}

		key := fmt.Sprintf("property%d", i)
		if has(out, key) {
			continue
		}

		// Counted as required, a property no value satisfies fails here, rather than adding none forever.
		required[key] = true

		if err := add(key, extra); err != nil {
			return nil, err
		}

		delete(required, key)
	}

	if boundedProps {
		for _, key := range slices.Backward(slices.Sorted(maps.Keys(out))) {
			if len(out) <= maxProps {
				break
			}

			if !required[key] {
				delete(out, key)
			}
		}

		if len(out) > maxProps {
			return nil, fmt.Errorf("%w: %s: %d required properties exceed maxProperties %d", ErrUnsatisfiable, at(path), len(out), maxProps)
		}
	}

	return out, nil
}

// patternSchema returns a schema that applies those of all the patternProperties of s that key matches, and whether
// one does.
func patternSchema(s map[string]any, key, path string) (any, bool, error) {
	patterns, _ := s["patternProperties"].(map[string]any)

	var (
		schema  any
		matched bool
	)

	for _, p := range slices.Sorted(maps.Keys(patterns)) {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %s: patternProperties %q: compiling: %w", ErrInvalid, at(path), p, err)
		}

		if re.MatchString(key) {
			schema, matched = bothSchemas(schema, patterns[p]), true
		}
	}

	return schema, matched, nil
}

// array returns an array of the items of s: one to three, within minItems and maxItems, or as few as allowed past
// _optionalDepth. Unique items stop at minItems once no new one comes. A list of items, as OpenAPI 3.0 and draft 4
// write a tuple, holds the prefixItems, and additionalItems the others.
func (w *walk) array(s map[string]any, name, path string, depth int) (any, error) {
	lo, _, err := intKeyword(s, "minItems", path)
	if err != nil {
		return nil, err
	}

	hi, bounded, err := intKeyword(s, "maxItems", path)
	if err != nil {
		return nil, err
	}

	if bounded && lo > hi {
		return nil, fmt.Errorf("%w: %s: minItems %d is above maxItems %d", ErrUnsatisfiable, at(path), lo, hi)
	}

	prefix, _ := s["prefixItems"].([]any)
	items := s["items"]

	if tuple, ok := items.([]any); ok {
		prefix, items = tuple, s["additionalItems"]
	}

	if items == nil {
		items = make(map[string]any)
	}

	if items == false {
		if lo > len(prefix) {
			return nil, fmt.Errorf("%w: %s: minItems %d is above the %d prefixItems allowed", ErrUnsatisfiable, at(path), lo, len(prefix))
		}

		if !bounded || hi > len(prefix) {
			hi, bounded = len(prefix), true
		}
	}

	if !bounded {
		hi = max(lo, 3)
	}

	n := lo
	if depth < _optionalDepth && hi > lo {
		n = max(lo, 1) + w.f.rnd.IntN(min(hi, max(lo, 1)+2)-max(lo, 1)+1)
	}

	unique := s["uniqueItems"] == true
	out := make([]any, 0, n)

	for i := range n {
		schema := items
		if i < len(prefix) {
			schema = prefix[i]
		}

		v, ok, err := w.item(schema, name, fmt.Sprintf("%s/%d", path, i), depth+1, out, unique)

		// An item past minItems no value satisfies ends the array.
		if errors.Is(err, ErrUnsatisfiable) && i >= lo {
			break
		}

		if err != nil {
			return nil, err
		}

		if !ok && i >= lo {
			break
		}

		if !ok {
			return nil, fmt.Errorf("%w: %s: expected %d unique items, found %d", ErrUnsatisfiable, at(path), lo, i)
		}

		out = append(out, v)
	}

	return out, nil
}

// item returns an item for an array that holds out already, and whether it found one: one out doesn't hold, when the
// items are unique.
func (w *walk) item(schema any, name, path string, depth int, out []any, unique bool) (any, bool, error) {
	for range _maxTries {
		v, err := w.value(schema, name, path, depth)
		if err != nil {
			return nil, false, err
		}

		if !unique || !slices.ContainsFunc(out, func(o any) bool { return same(o, v) }) {
			return v, true, nil
		}
	}

	return nil, false, nil
}

// str returns a string for s: of its pattern, its format, the name of its field or a few words, within its length.
func (f *Faker) str(s map[string]any, name, path string) (any, error) {
	lo, _, err := intKeyword(s, "minLength", path)
	if err != nil {
		return nil, err
	}

	hi, bounded, err := intKeyword(s, "maxLength", path)
	if err != nil {
		return nil, err
	}

	if bounded && lo > hi {
		return nil, fmt.Errorf("%w: %s: minLength %d is above maxLength %d", ErrUnsatisfiable, at(path), lo, hi)
	}

	exprs := patterns(s)
	res := make([]*regexp.Regexp, 0, len(exprs))

	for _, expr := range exprs {
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: pattern %q: compiling: %w", ErrInvalid, at(path), expr, err)
		}

		res = append(res, re)
	}

	fits := func(v string) bool {
		n := utf8.RuneCountInString(v)
		return n >= lo && (!bounded || n <= hi)
	}

	// A format's value is what makes it one: it isn't cut or padded to fit. One the patterns don't match gives way
	// to a string of the patterns.
	format, _ := s["format"].(string)

	v := f.format(format)
	known := v != ""

	if known {
		for range _maxTries {
			if fits(v) && matchAll(res, v) {
				return v, nil
			}

			v = f.format(format)
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("%w: %s: format %q makes no string of %s", ErrUnsatisfiable, at(path), format, lengths(lo, hi, bounded))
		}
	}

	if len(res) > 0 {
		// A format fakefy makes is one the string must be of, as the validator asserts it. A string of the patterns
		// that isn't of it may be with a value of the format after it, or before it, where the patterns don't anchor
		// it: x and jane@example.com make xjane@example.com, an email that ^x matches.
		check := validator(map[string]any{"format": format})

		for range _maxTries {
			v, err := f.pattern(res, lo, hi, bounded)
			if err != nil {
				return nil, fmt.Errorf("%w: %s: pattern %s: %w", ErrUnsatisfiable, at(path), quoteAll(exprs), err)
			}

			if !known || check == nil || validate(check, v) == nil {
				return v, nil
			}

			fv := f.format(format)
			for _, c := range []string{v + fv, fv + v} {
				if fits(c) && matchAll(res, c) && validate(check, c) == nil {
					return c, nil
				}
			}
		}

		return nil, fmt.Errorf("%w: %s: pattern %s: no string of format %q after %d tries", ErrUnsatisfiable, at(path), quoteAll(exprs), format, _maxTries)
	}

	v = f.byName(name)

	if v == "" {
		v = f.words(1 + f.rnd.IntN(2))
	}

	if n := utf8.RuneCountInString(v); n < lo {
		v += f.letters(lo - n)
	}

	if bounded && utf8.RuneCountInString(v) > hi {
		v = string([]rune(v)[:hi])
	}

	return v, nil
}

// patterns returns the patterns of s: one, or more an allOf merged.
func patterns(s map[string]any) []string {
	switch p := s["pattern"].(type) {
	case string:
		return []string{p}
	case []any:
		var out []string
		for _, item := range p {
			if expr, ok := item.(string); ok {
				out = append(out, expr)
			}
		}

		return out
	default:
		return nil
	}
}

// quoteAll quotes each of exprs, joined with " and ".
func quoteAll(exprs []string) string {
	quoted := make([]string, len(exprs))
	for i, expr := range exprs {
		quoted[i] = strconv.Quote(expr)
	}

	return strings.Join(quoted, " and ")
}

// lengths says how many characters lo, hi and bounded allow.
func lengths(lo, hi int, bounded bool) string {
	if bounded {
		return fmt.Sprintf("%d to %d characters", lo, hi)
	}

	return fmt.Sprintf("at least %d characters", lo)
}

// integer returns an integer within the bounds of s, a multiple of its multipleOf. Without bounds, the name of its
// field picks the range, or 1 to 100.
func (f *Faker) integer(s map[string]any, name, path string) (any, error) {
	one := big.NewRat(1, 1)

	step := one
	if m, ok := rat(s["multipleOf"]); ok && m.Sign() > 0 {
		// An integer that is a multiple of 2.5 is one of 5.
		step = lcm(one, m)
	}

	if step.Cmp(_maxInt) > 0 {
		return nil, fmt.Errorf("%w: %s: no integer is a multiple of %v", ErrUnsatisfiable, at(path), float(step))
	}

	lo, hi, exLo, exHi, err := bounds(s, name, path, step)
	if err != nil {
		return nil, err
	}

	klo, khi := indices(lo, hi, exLo, exHi, step)
	if klo.Cmp(khi) > 0 && step.Cmp(one) == 0 {
		return nil, fmt.Errorf("%w: %s: no integer between %v and %v", ErrUnsatisfiable, at(path), float(lo), float(hi))
	}

	if klo.Cmp(khi) > 0 {
		return nil, fmt.Errorf("%w: %s: no multiple of %v between %v and %v", ErrUnsatisfiable, at(path), float(step), float(lo), float(hi))
	}

	klo, khi = clamp(klo, khi, _minInt, _maxInt, step)
	if klo.Cmp(khi) > 0 {
		return nil, fmt.Errorf("%w: %s: integers from %v to %v don't fit an int64", ErrUnsatisfiable, at(path), float(lo), float(hi))
	}

	k := f.between(klo, khi)

	return k.Mul(k, step.Num()).Int64(), nil
}

// number returns a number within the bounds of s, with two decimals where they fit, a multiple of its multipleOf.
// Without bounds, the name of its field picks the range, or 1 to 100.
func (f *Faker) number(s map[string]any, name, path string) (any, error) {
	step, ok := rat(s["multipleOf"])
	if !ok || step.Sign() <= 0 {
		step = nil
	}

	lo, hi, exLo, exHi, err := bounds(s, name, path, step)
	if err != nil {
		return nil, err
	}

	if lo.Cmp(hi) == 0 && (exLo || exHi) {
		return nil, fmt.Errorf("%w: %s: no number between %v and %v, exclusive", ErrUnsatisfiable, at(path), float(lo), float(hi))
	}

	// in reports whether v is within the bounds as JSON writes it: a decimal, not a float64.
	in := func(v float64) bool {
		if math.IsInf(v, 0) {
			return false
		}

		d := decimal(v)
		cl, ch := d.Cmp(lo), d.Cmp(hi)

		return (cl > 0 || !exLo && cl == 0) && (ch < 0 || !exHi && ch == 0)
	}

	if step != nil {
		klo, khi := indices(lo, hi, exLo, exHi, step)
		klo, khi = clamp(klo, khi, _minFloat, _maxFloat, step)

		for range _maxTries {
			if klo.Cmp(khi) > 0 {
				break
			}

			// A float64 holds about 16 digits, so the decimal JSON writes for it may be another number: one past the
			// bounds, or not a multiple.
			k := new(big.Rat).SetInt(f.between(klo, khi))
			if v, _ := k.Mul(k, step).Float64(); in(v) && new(big.Rat).Quo(decimal(v), step).IsInt() {
				return v, nil
			}
		}

		return nil, fmt.Errorf("%w: %s: no multiple of %v between %v and %v", ErrUnsatisfiable, at(path), float(step), float(lo), float(hi))
	}

	// Halves, as hi - lo overflows to +Inf over the range of a float64.
	flo, fhi := float(lo), float(hi)
	mid, half := flo/2+fhi/2, fhi/2-flo/2

	v := mid + (2*f.rnd.Float64()-1)*half
	if r := math.Round(v*100) / 100; in(r) {
		return r, nil
	}

	if !in(v) {
		v = mid
	}

	return v, nil
}

// bounds returns the range of the number s describes, and whether each end excludes itself: its bounds, or the range
// the name of its field picks, or 1 to 100, moved to meet a single bound. A side s leaves open widens to hold a
// multiple of step, when step isn't nil.
func bounds(s map[string]any, name, path string, step *big.Rat) (lo, hi *big.Rat, exLo, exHi bool, err error) {
	flo, fhi, ok := numberRange(name)
	if !ok {
		flo, fhi = 1, 100
	}

	lo, hi = decimal(flo), decimal(fhi)
	width := new(big.Rat).Sub(hi, lo)

	minV, exLo, hasMin := lower(s)
	maxV, exHi, hasMax := upper(s)

	switch {
	case hasMin && hasMax:
		lo, hi = minV, maxV
	case hasMin:
		lo, hi = minV, greater(hi, new(big.Rat).Add(minV, width))
	case hasMax:
		lo, hi = lesser(lo, new(big.Rat).Sub(maxV, width)), maxV
	}

	switch {
	case step != nil && !hasMax:
		k := ceil(new(big.Rat).Quo(lo, step))
		hi = greater(hi, new(big.Rat).Mul(new(big.Rat).SetInt(k.Add(k, big.NewInt(2))), step))
	case step != nil && !hasMin:
		k := floor(new(big.Rat).Quo(hi, step))
		lo = lesser(lo, new(big.Rat).Mul(new(big.Rat).SetInt(k.Sub(k, big.NewInt(2))), step))
	}

	if lo.Cmp(hi) > 0 {
		return nil, nil, false, false, fmt.Errorf("%w: %s: minimum %v is above maximum %v", ErrUnsatisfiable, at(path), float(lo), float(hi))
	}

	return lo, hi, exLo, exHi, nil
}

// lower returns the lower bound of s, the stricter of minimum and exclusiveMinimum, and whether it excludes itself.
// JSON Schema 2020-12 and draft 6 on write exclusiveMinimum as a number; draft 4 and OpenAPI 3.0 as a boolean next to
// minimum.
func lower(s map[string]any) (v *big.Rat, excl, ok bool) {
	v, ok = rat(s["minimum"])
	excl = ok && s["exclusiveMinimum"] == true

	if x, isNum := rat(s["exclusiveMinimum"]); isNum && (!ok || x.Cmp(v) >= 0) {
		return x, true, true
	}

	return v, excl, ok
}

// upper returns the upper bound of s, as [lower] returns the lower one.
func upper(s map[string]any) (v *big.Rat, excl, ok bool) {
	v, ok = rat(s["maximum"])
	excl = ok && s["exclusiveMaximum"] == true

	if x, isNum := rat(s["exclusiveMaximum"]); isNum && (!ok || x.Cmp(v) <= 0) {
		return x, true, true
	}

	return v, excl, ok
}

// indices returns the least and the greatest k whose k × step is within lo and hi, each end excluding itself when
// exLo or exHi says so. The least is above the greatest when there is no such k.
func indices(lo, hi *big.Rat, exLo, exHi bool, step *big.Rat) (klo, khi *big.Int) {
	q := new(big.Rat).Quo(lo, step)
	if klo = ceil(q); exLo && q.IsInt() {
		klo.Add(klo, big.NewInt(1))
	}

	q = new(big.Rat).Quo(hi, step)
	if khi = floor(q); exHi && q.IsInt() {
		khi.Sub(khi, big.NewInt(1))
	}

	return klo, khi
}

// clamp narrows klo to khi, indices of multiples of step, to those of multiples within lo and hi.
func clamp(klo, khi *big.Int, lo, hi, step *big.Rat) (*big.Int, *big.Int) {
	if a := ceil(new(big.Rat).Quo(lo, step)); klo.Cmp(a) < 0 {
		klo = a
	}

	if b := floor(new(big.Rat).Quo(hi, step)); khi.Cmp(b) > 0 {
		khi = b
	}

	return klo, khi
}

// between returns an integer from lo to hi, lo at most hi. Past the span of a uint64, it is one of a float64's
// fractions of the span.
func (f *Faker) between(lo, hi *big.Int) *big.Int {
	span := new(big.Int).Sub(hi, lo)

	var off *big.Int

	switch {
	case span.IsUint64() && span.Uint64() == math.MaxUint64:
		off = new(big.Int).SetUint64(f.rnd.Uint64())
	case span.IsUint64():
		off = new(big.Int).SetUint64(f.rnd.Uint64N(span.Uint64() + 1))
	default:
		// With enough precision, the product is exact, and Int rounds it down, to at most span.
		frac := new(big.Float).SetPrec(uint(span.BitLen()) + 64).SetFloat64(f.rnd.Float64())
		off, _ = frac.Mul(frac, new(big.Float).SetInt(span)).Int(nil)
	}

	return off.Add(off, lo)
}

// floor returns r rounded down.
func floor(r *big.Rat) *big.Int {
	// The denominator is above 0, so the Euclidean quotient rounds down.
	q, _ := new(big.Int).DivMod(r.Num(), r.Denom(), new(big.Int))
	return q
}

// ceil returns r rounded up.
func ceil(r *big.Rat) *big.Int {
	q := floor(new(big.Rat).Neg(r))
	return q.Neg(q)
}

// lesser returns the lesser of a and b.
func lesser(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}

	return b
}

// greater returns the greater of a and b.
func greater(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}

	return b
}

// lcm returns the least number that is a multiple of a and of b, both above 0.
func lcm(a, b *big.Rat) *big.Rat {
	// lcm(p/q, r/s) is lcm(p, r) / gcd(q, s), of fractions in lowest terms.
	gcd := new(big.Int).GCD(nil, nil, a.Num(), b.Num())
	num := new(big.Int).Mul(a.Num(), b.Num())
	num.Div(num, gcd)

	den := new(big.Int).GCD(nil, nil, a.Denom(), b.Denom())

	return new(big.Rat).SetFrac(num, den)
}

// decimal returns x, a finite float64, as the decimal JSON writes it as, exactly.
func decimal(x float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(x, 'g', -1, 64))
	return r
}

// rat returns v as a fraction, exactly, when it is a finite number: any Go number, a json.Number, or the *big.Rat of a
// multipleOf an allOf merged. A float64 is the decimal JSON writes it as.
func rat(v any) (*big.Rat, bool) {
	if r, ok := v.(*big.Rat); ok {
		return r, true
	}

	if x, ok := number(v); !ok || math.IsInf(x, 0) || math.IsNaN(x) {
		return nil, false
	}

	return new(big.Rat).SetString(fmt.Sprint(v))
}

// float returns r as the nearest float64, for an error message.
func float(r *big.Rat) float64 {
	x, _ := r.Float64()
	return x
}

// merge adds the keywords of src to dst, as allOf has both apply: properties and required add up, bounds take the
// stricter, multiples the least common one, types, enums and consts what both allow, schemas both apply, and other
// keywords keep the value dst has. It fails when no value satisfies both.
func merge(dst, src map[string]any) error {
	src = maps.Clone(src)
	closeProperties(dst, src)
	closeProperties(src, dst)
	mergeTuples(dst, src)

	for _, k := range slices.Sorted(maps.Keys(src)) {
		v := src[k]

		prev, ok := dst[k]
		if !ok {
			dst[k] = v
			continue
		}

		switch k {
		case "properties", "patternProperties":
			dst[k] = mergeProperties(prev, v)
		case "required":
			old, _ := prev.([]any)
			add, _ := v.([]any)
			dst[k] = append(slices.Clone(old), add...)
		case "minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties":
			dst[k] = stricter(prev, v, 1)
		case "maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties":
			dst[k] = stricter(prev, v, -1)
		case "multipleOf":
			x, okX := rat(prev)
			y, okY := rat(v)

			if okX && okY && x.Sign() > 0 && y.Sign() > 0 {
				dst[k] = lcm(x, y)
			}
		case "type":
			t, err := intersectTypes(prev, v)
			if err != nil {
				return err
			}

			dst[k] = t
		case "enum":
			old, _ := prev.([]any)
			add, _ := v.([]any)

			both := slices.DeleteFunc(slices.Clone(old), func(x any) bool {
				return !slices.ContainsFunc(add, func(y any) bool { return same(x, y) })
			})
			if len(both) == 0 {
				return errors.New("no value is in both enums")
			}

			dst[k] = both
		case "const":
			if !same(prev, v) {
				return errors.New("two consts")
			}
		case "items", "additionalItems", "additionalProperties":
			dst[k] = bothSchemas(prev, v)
		case "prefixItems":
			old, _ := prev.([]any)
			add, _ := v.([]any)
			items := make([]any, max(len(old), len(add)))

			for i := range items {
				items[i] = bothSchemas(nth(old, i, nil), nth(add, i, nil))
			}

			dst[k] = items
		case "oneOf", "anyOf":
			// Each list picks a branch of its own: the one of dst first, then the other, from what's left of dst.
			all, _ := dst["allOf"].([]any)
			dst["allOf"] = append(slices.Clone(all), map[string]any{k: v})
		case "pattern":
			dst[k] = append(anys(patterns(dst)), anys(patterns(src))...)
		case "uniqueItems":
			dst[k] = prev == true || v == true
		}
	}

	return nil
}

// stricter returns the greater of the bounds a and b, for sign 1, or the lesser, for sign -1, or the one that is a
// number: a bound that isn't one is ignored.
func stricter(a, b any, sign int) any {
	x, okA := rat(a)
	y, okB := rat(b)

	switch {
	case !okA:
		return b
	case !okB || x.Cmp(y)*sign >= 0:
		return a
	default:
		return b
	}
}

// closeProperties applies the additionalProperties of a to the properties b names and a neither names nor matches by
// its patternProperties, as allOf has a apply to them. It changes the properties of b, a copy of them.
func closeProperties(a, b map[string]any) {
	extra, ok := a["additionalProperties"]
	if !ok {
		return
	}

	declared, _ := a["properties"].(map[string]any)
	props, _ := b["properties"].(map[string]any)

	closed := make(map[string]any, len(props))
	for name, schema := range props {
		// A patternProperties that doesn't compile fails where the object is made.
		if _, matched, _ := patternSchema(a, name, ""); has(declared, name) || matched {
			closed[name] = schema
		} else {
			closed[name] = bothSchemas(schema, extra)
		}
	}

	if len(props) > 0 {
		b["properties"] = closed
	}
}

// mergeTuples merges the items of dst and src when either is a list, as draft 4 and OpenAPI 3.0 write a tuple: item i
// applies the schema each gives it, its item i or, past the end of its list, its additionalItems, and items past both
// lists apply both additionalItems. An items schema that isn't a list gives every item. It takes items and
// additionalItems out of src.
func mergeTuples(dst, src map[string]any) {
	a, okA := dst["items"].([]any)
	b, okB := src["items"].([]any)

	if !okA && !okB {
		return
	}

	restA, restB := pastTuple(dst), pastTuple(src)
	items := make([]any, max(len(a), len(b)))

	for i := range items {
		items[i] = bothSchemas(nth(a, i, restA), nth(b, i, restB))
	}

	dst["items"], dst["additionalItems"] = items, bothSchemas(restA, restB)

	delete(src, "items")
	delete(src, "additionalItems")
}

// pastTuple returns the schema of the items of s past its tuple: its additionalItems, or its items when they aren't a
// list. Absent (nil), any item goes.
func pastTuple(s map[string]any) any {
	if _, ok := s["items"].([]any); ok {
		return s["additionalItems"]
	}

	return s["items"]
}

// mergeProperties returns the properties of a and of b, a property of both applying both schemas.
func mergeProperties(a, b any) map[string]any {
	props := make(map[string]any)
	old, _ := a.(map[string]any)
	maps.Copy(props, old)

	add, _ := b.(map[string]any)
	for name, schema := range add {
		props[name] = bothSchemas(props[name], schema)
	}

	return props
}

// bothSchemas returns a schema that applies schemas a and b, either of which may be absent (nil) or a boolean.
func bothSchemas(a, b any) any {
	switch {
	case a == nil || a == true:
		return b
	case b == nil || b == true:
		return a
	case a == false || b == false:
		return false
	default:
		return map[string]any{"allOf": []any{a, b}}
	}
}

// intersectTypes returns the types both type keywords, a and b, allow: an integer is a number too.
func intersectTypes(a, b any) (any, error) {
	x, y := types(a), types(b)

	var both []any
	for _, t := range x {
		switch {
		case slices.Contains(y, t):
			both = append(both, t)
		case t == "integer" && slices.Contains(y, "number"), t == "number" && slices.Contains(y, "integer"):
			both = append(both, "integer")
		}
	}

	switch len(both) {
	case 0:
		return nil, fmt.Errorf("no type is both of %q and %q", x, y)
	case 1:
		return both[0], nil
	default:
		return both, nil
	}
}

// types returns the types a type keyword, t, names.
func types(t any) []string {
	if name, ok := t.(string); ok {
		return []string{name}
	}

	list, _ := t.([]any)

	var out []string
	for _, item := range list {
		if name, ok := item.(string); ok {
			out = append(out, name)
		}
	}

	return out
}

// anys returns strs as a []any.
func anys(strs []string) []any {
	out := make([]any, len(strs))
	for i, s := range strs {
		out[i] = s
	}

	return out
}

// nth returns item i of list, or rest past its end.
func nth(list []any, i int, rest any) any {
	if i < len(list) {
		return list[i]
	}

	return rest
}

// same reports whether a and b are the same as JSON: numbers equal by value, exactly, whatever their Go type.
func same(a, b any) bool {
	if x, ok := rat(a); ok {
		y, ok := rat(b)
		return ok && x.Cmp(y) == 0
	}

	switch a := a.(type) {
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}

		for k, v := range a {
			if w, ok := b[k]; !ok || !same(v, w) {
				return false
			}
		}

		return true
	case []any:
		b, ok := b.([]any)
		return ok && slices.EqualFunc(a, b, same)
	default:
		// a is a string, a bool, nil or a float64 that isn't finite: comparable, so == doesn't panic.
		return a == b
	}
}

// clone returns a copy of v, a value of a schema, that shares nothing with it, as decoded JSON: numbers are int64,
// when integers, or float64.
func clone(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = clone(x)
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = clone(x)
		}

		return out
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint:
		return clone(uint64(v))
	case uint64:
		if v > math.MaxInt64 {
			return float64(v)
		}

		return int64(v)
	case float32:
		return float64(v)
	case interface{ Int64() (int64, error) }:
		// A json.Number.
		if i, err := v.Int64(); err == nil {
			return i
		}

		f, _ := number(v)

		return f
	default:
		return v
	}
}

// typesOf returns the types of the values s describes, depth levels down, in the order to try them: those of its
// type keyword, null last, or first from _optionalDepth on, so a nullable schema that nests itself ends; or the type
// its keywords imply.
func typesOf(s map[string]any, depth int) []string {
	listed := types(s["type"])
	out := slices.DeleteFunc(slices.Clone(listed), func(t string) bool { return t == "null" })

	switch {
	case len(out) == len(listed):
	case depth >= _optionalDepth:
		out = append([]string{"null"}, out...)
	default:
		out = append(out, "null")
	}

	if len(out) > 0 {
		return out
	}

	hasAny := func(keys ...string) bool {
		return slices.ContainsFunc(keys, func(k string) bool { return has(s, k) })
	}

	switch {
	case hasAny("properties", "additionalProperties", "required", "minProperties", "maxProperties"):
		return []string{"object"}
	case hasAny("items", "prefixItems", "minItems", "maxItems", "uniqueItems"):
		return []string{"array"}
	case hasAny("minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"):
		return []string{"number"}
	default:
		return []string{"string"}
	}
}

// schemaMap returns s as a schema object: true is the empty schema, and false fails, as no value satisfies it.
func schemaMap(s any, path string) (map[string]any, error) {
	switch s := s.(type) {
	case map[string]any:
		return s, nil
	case bool:
		if s {
			return make(map[string]any), nil
		}

		return nil, fmt.Errorf("%w: %s: the schema is false", ErrUnsatisfiable, at(path))
	default:
		return nil, fmt.Errorf("%w: %s: expected a schema object or boolean, not %T", ErrInvalid, at(path), s)
	}
}

// number returns v as a float64, when it is a number: any Go number, or a json.Number.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int16:
		return float64(n), true
	case int8:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint:
		return float64(n), true
	case interface{ Float64() (float64, error) }:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// has reports whether m holds key.
func has(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// intKeyword returns the keyword key of s, a count such as minLength, when s has it. One past _maxCount counts as
// _maxCount.
func intKeyword(s map[string]any, key, path string) (int, bool, error) {
	n, ok := number(s[key])
	if !ok {
		return 0, false, nil
	}

	if n < 0 || n != math.Trunc(n) {
		return 0, false, fmt.Errorf("%w: %s: %s must be a whole number from 0, not %v", ErrInvalid, at(path), key, n)
	}

	return int(min(n, _maxCount)), true, nil
}

// escape writes key as a JSON pointer token.
func escape(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// at names path, the JSON pointer of a value, in an error.
func at(path string) string {
	if path == "" {
		return "at the root"
	}

	return "at " + strconv.Quote(path)
}
