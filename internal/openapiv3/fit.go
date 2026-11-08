package openapiv3

import (
	json "encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/caffeine-projects/vlatte/internal/value"
)

// fitKey names an example fit checked: where it is, the schema it checked it against, and whether as the body of a
// response.
type fitKey struct {
	place    string
	schema   *openapi3.Schema
	response bool
}

// fitted is what fit returned for an example.
type fitted struct {
	v  any
	ok bool
}

// fit returns v, the example of field f, followed by more, such as the index of an example of a list, when it fits
// schema, as the body of a response when response is set, as kin-openapi checks an example; else v converted to fit
// it, as coerce does; else false, for the example to be left out. It logs an example that doesn't fit, with what it
// converted or what the example breaks, once for its place, schema and use, when the spec gives its place. Without a
// schema, v fits.
func (c converter) fit(v any, schema *openapi3.SchemaRef, response bool, f field, more string) (any, bool) {
	if schema == nil || schema.Value == nil {
		return v, true
	}

	place := c.place(f, more)

	key := fitKey{place: place, schema: schema.Value, response: response}
	if r, ok := c.fits[key]; ok {
		return r.v, r.ok
	}

	opts := []openapi3.SchemaValidationOption{openapi3.MultiErrors()}
	if response {
		opts = append(opts, openapi3.VisitAsResponse())
	}

	if !c.v30 {
		opts = append(opts, openapi3.EnableJSONSchema2020())
	}

	err := schema.Value.VisitJSON(v, opts...)
	if err == nil {
		return v, true
	}

	r := fitted{}

	var changes []string

	written := func(ptr string) (string, bool) { return c.places.text(f, more+ptr) }
	if converted, ok := coerce(v, schema.Value, "", written, &changes); ok {
		err = schema.Value.VisitJSON(converted, opts...)
		if err == nil {
			r = fitted{v: converted, ok: true}
			c.log.Warn().Str("example", place).Msgf("the example does not fit its schema: converted %s", strings.Join(changes, ", "))
		}
	}

	if !r.ok {
		c.log.Warn().Str("example", place).Msgf("the example breaks its schema's %s: left out", strings.Join(breaks(err), ", "))
	}

	// A place the spec doesn't give names no example.
	if place != "" {
		c.fits[key] = r
	}

	return r.v, r.ok
}

// place returns where field f is in the spec, followed by more, such as the index of an example of a list, or "" when
// the spec doesn't give its object's place.
func (c converter) place(f field, more string) string {
	at := c.places.of(f)
	if at == "" {
		return ""
	}

	return at + "/" + pointerToken(f.name) + more
}

// fitSchema fits the example and the examples of schema v, ref as JSON, and of each schema within it, as fit does: it
// converts or leaves out, in v, those that don't fit. What a $ref names is fitted where it is written, but for the
// keywords beside the $ref, which kin-openapi writes back: those its example and examples are checked against what it
// names, and, in OpenAPI 3.1, which reads the keywords that aren't annotations as one more allOf entry of what it
// names, its last, those of the schemas within that entry.
func (c converter) fitSchema(v any, ref *openapi3.SchemaRef) {
	m, ok := v.(map[string]any)
	if !ok || ref == nil || ref.Value == nil {
		return
	}

	s := ref.Value

	// The example beside a $ref is the $ref's: in OpenAPI 3.0, s is what it names, whose example is another.
	var owner any = s
	if ref.Ref != "" {
		owner = ref
	}

	f := field{object: owner, name: "example"}
	if ex, ok := m[f.name]; ok {
		if ex, ok = c.fit(ex, ref, false, f, ""); ok {
			m[f.name] = ex
		} else {
			delete(m, f.name)
		}
	}

	f = field{object: owner, name: "examples"}
	if list, ok := m[f.name].([]any); ok && len(list) > 0 {
		var kept []any

		for i, ex := range list {
			if ex, ok := c.fit(ex, ref, false, f, "/"+strconv.Itoa(i)); ok {
				kept = append(kept, ex)
			}
		}

		if len(kept) > 0 {
			m[f.name] = kept
		} else {
			delete(m, f.name)
		}
	}

	if ref.Ref != "" {
		if c.v30 || len(s.AllOf) == 0 {
			return
		}

		s = s.AllOf[len(s.AllOf)-1].Value
		if s == nil {
			return
		}
	}

	one, lists, named := schemasIn(s)

	for _, name := range slices.Sorted(maps.Keys(one)) {
		c.fitSchema(m[name], one[name])
	}

	for _, name := range slices.Sorted(maps.Keys(lists)) {
		items, _ := m[name].([]any)
		for i, sub := range lists[name] {
			if i < len(items) {
				c.fitSchema(items[i], sub)
			}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(named)) {
		schemas, _ := m[name].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(named[name])) {
			c.fitSchema(schemas[key], named[name][key])
		}
	}
}

// coerce returns v converted to fit schema s, and whether it converted any of it: each scalar of a type s doesn't
// allow becomes the first of its types it reads as, as convert does, written giving the text the spec writes for the
// scalar at a JSON pointer within v. It walks the allOf of s, its properties and additionalProperties, and its items,
// and adds each scalar it converts to changes, with where it is, ptr, the JSON pointer of v. It shares with v what it
// doesn't convert.
func coerce(v any, s *openapi3.Schema, ptr string, written func(ptr string) (string, bool), changes *[]string) (any, bool) {
	if s == nil {
		return v, false
	}

	converted := false

	for _, sub := range s.AllOf {
		if sub == nil {
			continue
		}

		var ok bool
		if v, ok = coerce(v, sub.Value, ptr, written, changes); ok {
			converted = true
		}
	}

	switch x := v.(type) {
	case map[string]any:
		var out map[string]any

		for _, k := range slices.Sorted(maps.Keys(x)) {
			sub := s.Properties[k]
			if sub == nil {
				sub = s.AdditionalProperties.Schema
			}

			if sub == nil {
				continue
			}

			if item, ok := coerce(x[k], sub.Value, ptr+"/"+pointerToken(k), written, changes); ok {
				if out == nil {
					out = maps.Clone(x)
				}

				out[k] = item
			}
		}

		if out != nil {
			return out, true
		}
	case []any:
		if s.Items == nil {
			break
		}

		var out []any

		for i, item := range x {
			if item, ok := coerce(item, s.Items.Value, ptr+"/"+strconv.Itoa(i), written, changes); ok {
				if out == nil {
					out = slices.Clone(x)
				}

				out[i] = item
			}
		}

		if out != nil {
			return out, true
		}
	default:
		if to, from, ok := convert(x, s, func() (string, bool) { return written(ptr) }); ok {
			change := from + " to " + text(to)
			if ptr != "" {
				change = from + " at " + ptr + " to " + text(to)
			}

			*changes = append(*changes, change)

			return to, true
		}
	}

	return v, converted
}

// convert returns scalar v as the first type of schema s it reads as, when s doesn't allow the type of v, and v as
// the spec writes it: a number or a boolean is the string of the text the spec writes for it, which written gives,
// such as 1.0 or 01234, and a string that is the JSON text of a number or of a boolean is that value.
func convert(v any, s *openapi3.Schema, written func() (string, bool)) (to any, from string, ok bool) {
	if s.Type.IsEmpty() || allows(s, v) {
		return nil, "", false
	}

	for _, t := range *s.Type {
		switch x := v.(type) {
		case bool, int64, float64:
			if t != openapi3.TypeString {
				continue
			}

			// Its value, read back, may be written otherwise: 1.0 is 1.
			if w, ok := written(); ok {
				return w, w, true
			}

			return nil, "", false
		case string:
			if parsed, ok := scalar(x); ok && (typeOf(parsed) == t || typeOf(parsed) == openapi3.TypeInteger && t == openapi3.TypeNumber) {
				return parsed, text(x), true
			}
		}
	}

	return nil, "", false
}

// scalar returns the number or the boolean s is the JSON text of.
func scalar(s string) (any, bool) {
	switch s {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return value.ParseNumber(s)
	}
}

// allows reports whether schema s allows the JSON type of v, an integer as a number too.
func allows(s *openapi3.Schema, v any) bool {
	t := typeOf(v)

	return s.Type.Permits(t) || t == openapi3.TypeInteger && s.Type.Includes(openapi3.TypeNumber) || t == openapi3.TypeNull && s.Nullable
}

// typeOf returns the JSON Schema type of v, a value of the spec.
func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return openapi3.TypeNull
	case bool:
		return openapi3.TypeBoolean
	case string:
		return openapi3.TypeString
	case int64:
		return openapi3.TypeInteger
	case float64:
		return openapi3.TypeNumber
	case []any:
		return openapi3.TypeArray
	default:
		return openapi3.TypeObject
	}
}

// text returns the JSON text of v, a value of the spec.
func text(v any) string {
	data, _ := json.Marshal(v)

	return string(data)
}

// breaks returns what err, of kin-openapi's check of a value against a schema, says the value breaks: each keyword,
// followed by where in the value when not at its root, such as minimum at /n, sorted, without repeats.
func breaks(err error) []string {
	var out []string

	var walk func(err error)

	walk = func(err error) {
		if multi, ok := errors.AsType[openapi3.MultiError](err); ok {
			for _, e := range multi {
				walk(e)
			}

			return
		}

		se, ok := errors.AsType[*openapi3.SchemaError](err)
		if !ok {
			out = append(out, err.Error())
			return
		}

		b := se.SchemaField
		if ptr := se.JSONPointer(); len(ptr) > 0 {
			tokens := make([]string, len(ptr))
			for i, t := range ptr {
				tokens[i] = pointerToken(t)
			}

			b += " at /" + strings.Join(tokens, "/")
		}

		out = append(out, b)
	}

	walk(err)
	slices.Sort(out)

	return slices.Compact(out)
}
