package vlatte

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"maps"
	"reflect"

	"github.com/ohler55/ojg/jp"
)

// jsonDocument returns what JSONPath functions read of v: JSON text parsed, and any other value as it is.
func jsonDocument(v any) (any, error) {
	var text []byte

	switch t := v.(type) {
	case string:
		text = []byte(t)
	case []byte:
		text = t
	default:
		return v, nil
	}

	var doc any
	if err := json.Unmarshal(text, &doc); err != nil {
		return nil, err
	}

	return doc, nil
}

// jsonPath returns the first value path selects in v, a value or JSON text, or nil when it selects none.
func jsonPath(path string, v any) (any, error) {
	all, err := jsonPathAll(path, v)
	if err != nil || len(all) == 0 {
		return nil, err
	}

	return all[0], nil
}

// jsonPathAll returns every value path selects in v, a value or JSON text.
func jsonPathAll(path string, v any) ([]any, error) {
	x, err := jp.ParseString(path)
	if err != nil {
		return nil, err
	}

	doc, err := jsonDocument(v)
	if err != nil {
		return nil, err
	}

	return selectAll(x, doc), nil
}

// selectAll returns every value x selects in v, which is never parsed: a string is a value.
func selectAll(x jp.Expr, v any) []any {
	if all := x.Get(v); all != nil {
		return all
	}

	return []any{}
}

// deleteJSONPath returns a copy of v, a value or JSON text, without the values path selects. v itself is unchanged.
func deleteJSONPath(path string, v any) (any, error) {
	x, err := jp.ParseString(path)
	if err != nil {
		return nil, err
	}

	doc, err := jsonDocument(v)
	if err != nil {
		return nil, err
	}

	r, err := x.Remove(copyJSON(doc))
	if err != nil {
		return nil, err
	}

	return r, nil
}

// copyJSON returns a deep copy of the maps with string keys and the slices in v, as map[string]any and []any, so
// a JSONPath can change it. Other values, nil among them, stay as they are.
func copyJSON(v any) any {
	rv := reflect.ValueOf(v)

	switch {
	case rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String:
		m := make(map[string]any, rv.Len())
		for it := rv.MapRange(); it.Next(); {
			m[it.Key().String()] = copyJSON(it.Value().Interface())
		}

		return m
	case rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() != reflect.Uint8:
		s := make([]any, rv.Len())
		for i := range s {
			s[i] = copyJSON(rv.Index(i).Interface())
		}

		return s
	default:
		return v
	}
}

// mergePatch applies patch to v as a JSON Merge Patch, RFC 7386, and returns the result: an object patch merges
// into an object, recursively, its null values removing keys, and any other patch replaces v. Each of patch and v is a
// value or JSON text, so a string is JSON text, and v is unchanged.
func mergePatch(patch, v any) (any, error) {
	p, err := jsonDocument(patch)
	if err != nil {
		return nil, err
	}

	doc, err := jsonDocument(v)
	if err != nil {
		return nil, err
	}

	return mergeValues(p, doc), nil
}

// mergeValues applies patch to v as mergePatch does, on values.
func mergeValues(patch, v any) any {
	p, ok := jsonObject(patch)
	if !ok {
		return patch
	}

	target, ok := jsonObject(v)
	if !ok {
		target = make(map[string]any, len(p))
	}

	for k, pv := range p {
		if pv == nil {
			delete(target, k)
			continue
		}

		target[k] = mergeValues(pv, target[k])
	}

	return target
}

// jsonObject returns a copy of a map with string keys, such as a JSON object, as a map[string]any.
func jsonObject(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return maps.Clone(m), true
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	m := make(map[string]any, rv.Len())
	for it := rv.MapRange(); it.Next(); {
		m[it.Key().String()] = it.Value().Interface()
	}

	return m, true
}

// indentJSON reformats JSON text with each item on a line, indented by two spaces, its keys in their order.
func indentJSON(s string) (string, error) {
	v := jsontext.Value(s)
	if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
		return "", err
	}

	return string(v), nil
}

// compactJSON reformats JSON text without white space, its keys in their order.
func compactJSON(s string) (string, error) {
	v := jsontext.Value(s)
	if err := v.Compact(); err != nil {
		return "", err
	}

	return string(v), nil
}
