package matchopenapi

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

// _bodyURL is where the schema of a body is, for its compiler: a name, which nothing loads from.
const _bodyURL = "mem://openapi/body.json"

// body is how the matcher checks a request body in a media type of an operation: against its schema as the spec
// writes it, with JSON Schema 2020-12.
type body struct {
	mediaType *openapi3.MediaType                // as kin-openapi read it, which reads a body other than JSON
	schema    func() (*jsonschema.Schema, error) // the schema compiled, once a body needs it
}

// newBody returns how the matcher checks a body of mt, whose schema is root, as openapiv3.RequestBodies gives it, of a
// spec of OpenAPI 3.0 when v30 is true. It drops each pattern Go can't compile from root, which can't be checked: the
// rest of the body still is.
func newBody(mt *openapi3.MediaType, root map[string]any, v30 bool) *body {
	formats := make(map[string]bool)
	prepare(root, formats, v30)

	return &body{mediaType: mt, schema: sync.OnceValues(func() (*jsonschema.Schema, error) { return compile(root, formats) })}
}

// prepare drops from schema v, and the schemas within it, what kin-openapi doesn't check: each pattern Go can't
// compile, of pattern or patternProperties, and, when v30 is true, for OpenAPI 3.0, an integer format of a schema
// whose type isn't integer alone. It adds to formats the formats they name.
func prepare(v any, formats map[string]bool, v30 bool) {
	switch x := v.(type) {
	case map[string]any:
		if p, ok := x["pattern"].(string); ok && !compiles(p) {
			delete(x, "pattern")
		}

		if f, ok := x["format"].(string); ok && v30 && isIntegerFormat(f) && !integerOnly(x["type"]) {
			delete(x, "format")
		}

		if f, ok := x["format"].(string); ok {
			formats[f] = true
		}

		if named, ok := x["patternProperties"].(map[string]any); ok {
			for p := range named {
				if !compiles(p) {
					delete(named, p)
				}
			}
		}

		for k, val := range x {
			switch {
			case slices.Contains(openapi.ValueKeywords, k):
				// Data, whatever keys it holds.
			case slices.Contains(openapi.SchemaMapKeywords, k):
				// Names, not keywords: a property named pattern is a schema.
				named, _ := val.(map[string]any)
				for _, s := range named {
					prepare(s, formats, v30)
				}
			default:
				prepare(val, formats, v30)
			}
		}
	case []any:
		for _, item := range x {
			prepare(item, formats, v30)
		}
	}
}

// isIntegerFormat reports whether kin-openapi checks the format f of an integer, and not of a number.
func isIntegerFormat(f string) bool {
	_, integer := openapi3.SchemaIntegerFormats[f]
	_, number := openapi3.SchemaNumberFormats[f]

	return integer && !number
}

// integerOnly reports whether t, the type of a schema, takes integers and no other number: integer, or a list of
// types with integer and without number.
func integerOnly(t any) bool {
	switch x := t.(type) {
	case string:
		return x == "integer"
	case []any:
		return slices.Contains(x, any("integer")) && !slices.Contains(x, any("number"))
	default:
		return false
	}
}

// compiles reports whether Go compiles the regular expression p, as the validator does.
func compiles(p string) bool {
	_, err := regexp.Compile(p)
	return err == nil
}

// compile compiles root, a JSON Schema 2020-12 document, asserting each of formats as kin-openapi does: a format it
// knows, such as date, is checked, and any other, which OpenAPI lets be a note, isn't.
func compile(root map[string]any, formats map[string]bool) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	// Every reference of the spec is local: a schema loads nothing.
	c.UseLoader(jsonschema.SchemeURLLoader{})

	for name := range formats {
		c.RegisterFormat(&jsonschema.Format{Name: name, Validate: kinFormat(name)})
	}

	c.AssertFormat()

	if err := c.AddResource(_bodyURL, root); err != nil {
		return nil, err
	}

	return c.Compile(_bodyURL)
}

// kinFormat returns how kin-openapi checks a value of the format name: a string by its formats of strings, and a
// number by its formats of integers when it is whole, else of numbers. A value of a format it doesn't know is valid.
func kinFormat(name string) func(v any) error {
	return func(v any) error {
		switch x := v.(type) {
		case string:
			if f, ok := openapi3.SchemaStringFormats[name]; ok {
				return f.Validate(x)
			}
		case interface{ Int64() (int64, error) }:
			// A json.Number, as JSON writes it.
			if i, err := x.Int64(); err == nil {
				return kinNumberFormat(name, float64(i), &i)
			}

			if f, err := strconv.ParseFloat(fmt.Sprint(x), 64); err == nil {
				return kinNumberFormat(name, f, nil)
			}
		case float64:
			// A number of a form, as kin-openapi reads it.
			return kinNumberFormat(name, x, nil)
		}

		return nil
	}
}

// kinNumberFormat checks f, a number of the format name, or i, the same one exactly, when it is an integer an int64
// holds, as kin-openapi does.
func kinNumberFormat(name string, f float64, i *int64) error {
	if i == nil && f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 {
		n := int64(f)
		i = &n
	}

	if check, ok := openapi3.SchemaIntegerFormats[name]; ok && i != nil {
		return check.Validate(*i)
	}

	if check, ok := openapi3.SchemaNumberFormats[name]; ok {
		return check.Validate(f)
	}

	return nil
}

// check returns what data, a body of b's media type, with the headers header, breaks, or nil when it keeps its schema.
// A body kin-openapi reads nothing of, such as XML, isn't checked. It returns false when it can't check it, as its
// schema doesn't compile.
func (b *body) check(data []byte, header http.Header) ([]string, bool) {
	sch, err := b.schema()
	if err != nil {
		return nil, false
	}

	value, read, err := b.decode(data, header)
	if pe, ok := errors.AsType[*openapi3filter.ParseError](err); !read || ok && pe.Kind == openapi3filter.KindUnsupportedFormat {
		return nil, true
	}

	if err != nil {
		return []string{"body: decoding request body: " + err.Error()}, true
	}

	verr, ok := errors.AsType[*jsonschema.ValidationError](sch.Validate(value))
	if !ok {
		return nil, true
	}

	return bodyReasons(verr), true
}

// decode returns data, a body of b's media type, with the headers header, as kin-openapi reads it, but a JSON one
// exactly, each number as written: a binary string of any media type as the text of data. It reports whether
// kin-openapi reads that media type.
func (b *body) decode(data []byte, header http.Header) (any, bool, error) {
	if s := b.mediaType.Schema; s != nil && s.Value != nil && s.Value.Type.Is(openapi3.TypeString) && s.Value.Format == "binary" {
		return string(data), true, nil
	}

	mediaType, _, _ := mime.ParseMediaType(header.Get("Content-Type"))
	if openapi.IsJSON(mediaType) {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		return v, true, err
	}

	decoder := openapi3filter.RegisteredBodyDecoder(mediaType)
	if decoder == nil {
		decoder = openapi3filter.RegisteredBodyDecoder("*/*")
	}

	if decoder == nil {
		return nil, false, nil
	}

	v, err := decoder(bytes.NewReader(data), header, b.mediaType.Schema, func(name string) *openapi3.Encoding {
		return b.mediaType.Encoding[name]
	})

	return v, true, err
}

// bodyReasons returns what verr, the error of a body against its schema, says the body breaks: one message for each
// thing, as kin-openapi words it, sorted, as the validator finds them in no fixed order.
func bodyReasons(verr *jsonschema.ValidationError) []string {
	errs := unwrap(verr)
	out := make([]string, 0, len(errs))

	for _, failing := range errs {
		var b strings.Builder
		for _, token := range failing.InstanceLocation {
			b.WriteString("/" + strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
		}

		at := b.String()

		subject := "body"
		if at != "" {
			subject += " at " + at
		}

		out = append(out, subject+": "+message(failing, at))
	}

	slices.Sort(out)

	return out
}

// unwrap returns the errors e groups, such as those of the schema a $ref names, or of each schema of an allOf, in
// turn, or e, when it groups none: a body breaks what they say, where they say it.
func unwrap(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if !wraps(e.ErrorKind) || len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}

	var out []*jsonschema.ValidationError
	for _, cause := range e.Causes {
		out = append(out, unwrap(cause)...)
	}

	return out
}

// wraps reports whether an error of kind k only groups the errors of its causes.
func wraps(k jsonschema.ErrorKind) bool {
	switch k.(type) {
	case *kind.Schema, *kind.Group, *kind.Reference, *kind.AllOf:
		return true
	default:
		return false
	}
}

// message returns why e, about the value at, fails, in one line: the first of the validator's, which lists below it
// why each schema of a oneOf fails, say, without the location it starts with, such as at '/age': . A number is
// written exactly, and null as JSON writes it.
func message(e *jsonschema.ValidationError, at string) string {
	switch k := e.ErrorKind.(type) {
	case *kind.Minimum:
		return bound("minimum", k.Got, k.Want)
	case *kind.Maximum:
		return bound("maximum", k.Got, k.Want)
	case *kind.ExclusiveMinimum:
		return bound("exclusiveMinimum", k.Got, k.Want)
	case *kind.ExclusiveMaximum:
		return bound("exclusiveMaximum", k.Got, k.Want)
	case *kind.MultipleOf:
		return bound("multipleOf", k.Got, k.Want)
	case *kind.Const:
		if k.Want == nil {
			return "value must be null"
		}
	case *kind.Enum:
		if slices.Contains(k.Want, nil) && !slices.ContainsFunc(k.Want, composite) {
			values := make([]string, len(k.Want))
			for i, v := range k.Want {
				values[i] = display(v)
			}

			if len(values) == 1 {
				return "value must be " + values[0]
			}

			return "value must be one of " + strings.Join(values, ", ")
		}
	}

	line, _, _ := strings.Cut(e.Error(), "\n")

	return strings.TrimPrefix(line, "at '"+at+"': ")
}

// bound returns the message of a number that breaks the bound keyword, both numbers exact.
func bound(keyword string, got, want *big.Rat) string {
	return fmt.Sprintf("%s: got %s, want %s", keyword, number(got), number(want))
}

// number returns r as JSON writes it: an integer whole, whatever its size, and any other number as the float64
// nearest it.
func number(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}

	f, _ := r.Float64()

	return strconv.FormatFloat(f, 'g', -1, 64)
}

// composite reports whether v is an object or a list.
func composite(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

// display returns v, a value of an enum that isn't an object or a list, as the validator writes it, but null as JSON
// writes it: a string in single quotes.
func display(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		q := strings.ReplaceAll(strings.ReplaceAll(strconv.Quote(x), `\"`, `"`), `'`, `\'`)
		return "'" + q[1:len(q)-1] + "'"
	default:
		return fmt.Sprint(x)
	}
}
