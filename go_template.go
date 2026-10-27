package vlatte

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/caffeine-projects/vlatte/internal/tmplfunc"
)

var _ TemplateEngine = (*GoTemplateEngine)(nil)

// _missingKeyModes are the missingkey options of text/template.
var _missingKeyModes = []string{"default", "invalid", "zero", "error"}

// GoTemplateEngine compiles Go templates, of package text/template. Servers use it when no other engine is set.
// It is safe for concurrent use. Create one with NewGoTemplateEngine.
//
// text/template, and not html/template, is the package for mocks: templates write bodies of any type, such as JSON
// and XML, and headers, URLs and file names, which html/template would escape for HTML. Escape on purpose with the
// html, js and urlquery functions, or with toJSON for a JSON value.
//
// Besides the functions of text/template, templates can call those below. A function's last argument is the value it
// works on, so it pipes, as in {{ .Request.Header.Get "X-Name" | trimSpace | toUpper }}, but for the list functions,
// which take the list first, as Go's append does. A function given a bad argument, such as an unknown algorithm or a
// number that isn't one, fails the template, so a mock never answers with a broken body.
//   - JSON: toJSON (a value as JSON, such as a quoted and escaped string), fromJSON, jsonPath and jsonPathAll (what a
//     JSONPath selects), deleteJSONPath and mergePatch (RFC 7386), which read a value or JSON text, and indentJSON
//     and compactJSON.
//   - Strings: toUpper, toLower, trimSpace, title, upperFirst, truncate, replaceAll, split, join, substring and slug,
//     which count runes, not bytes; regexpFind, regexpSubmatch, regexpReplaceAll and regexpMatch, with Go's regexp
//     syntax; contains, for a substring, a list item or a map key; default, for a value that is nil, "" or empty.
//   - Numbers: add, sub, mul, div and mod, on ints when both operands are integers and on float64s otherwise, and
//     toInt and toFloat, which convert numbers and numeric strings, such as a query parameter.
//   - Lists: list, append, insert, remove, randomItem, shuffle, reverse and sortBy, which return new lists.
//   - XML: xpath, the text of what an XPath 1.0 expression selects, and xpathNodes, the nodes it selects.
//   - Time: now, the current time.Time; formatTime and parseTime, with Go layouts, the names of those of package
//     time, such as RFC3339, and unix and unixMilli; timeAdd, by a Go duration or such as "3 days"; inZone; and
//     truncateTime, such as to "first day of month".
//   - Encoding: base64Encode and base64Decode, queryUnescape and parseQuery.
//   - Random: uuid, randomString, randomInt and randomFloat. They are not for secrets.
//   - Hashes: hash, such as {{ hash "sha256" "hex" .Request.Body.String }}, and hmac.
//   - env: the value of an environment variable WithEnv allows. A name env is given as a literal and isn't allowed
//     fails the template when it compiles.
//
// A missing map key is an error, so a typo doesn't render as "<no value>". Read an optional key with index, which
// gives the zero value, as in {{ with index .Extra "name" }}{{ . }}{{ end }}, or change the rule with WithMissingKey.
type GoTemplateEngine struct {
	funcs      map[string]any
	missingKey string
	env        tmplfunc.Env
	customEnv  bool // whether WithTemplateFuncs replaced env, so Compile doesn't check its names
}

// GoTemplateOption configures a GoTemplateEngine.
type GoTemplateOption func(*GoTemplateEngine) error

// NewGoTemplateEngine creates a GoTemplateEngine.
func NewGoTemplateEngine(opts ...GoTemplateOption) (*GoTemplateEngine, error) {
	e := &GoTemplateEngine{funcs: tmplfunc.Funcs(), missingKey: "error"}
	e.funcs["env"] = e.env.Get

	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, fmt.Errorf("vlatte: go template engine: %w", err)
		}
	}

	return e, nil
}

// MustNewGoTemplateEngine is like NewGoTemplateEngine but panics if an option fails.
func MustNewGoTemplateEngine(opts ...GoTemplateOption) *GoTemplateEngine {
	e, err := NewGoTemplateEngine(opts...)
	if err != nil {
		panic(err)
	}

	return e
}

// WithEnv allows templates to read the environment variables names with env. Names are exact and case-sensitive, and
// each call adds to the names allowed. Without it, templates read none: env fails for every name.
func WithEnv(names ...string) GoTemplateOption {
	return func(e *GoTemplateEngine) error { return e.env.Allow(names...) }
}

// WithTemplateFuncs adds functions templates can call, by name. A function replaces a built-in one of the same
// name. Each must be a function text/template can call: one result, or two with an error second.
func WithTemplateFuncs(funcs map[string]any) GoTemplateOption {
	return func(e *GoTemplateEngine) error {
		if err := checkFuncs(funcs); err != nil {
			return err
		}

		maps.Copy(e.funcs, funcs)

		if _, ok := funcs["env"]; ok {
			e.customEnv = true
		}

		return nil
	}
}

// WithMissingKey sets what a template does with a missing map key, as the missingkey option of text/template:
// "error" stops it with an error, the default; "default" and "invalid" print "<no value>"; "zero" prints the zero
// value of the map's element type, which is "<no value>" too for a map[string]any.
func WithMissingKey(mode string) GoTemplateOption {
	return func(e *GoTemplateEngine) error {
		if !slices.Contains(_missingKeyModes, mode) {
			return fmt.Errorf("missing key mode %q is not one of %s", mode, strings.Join(_missingKeyModes, ", "))
		}

		e.missingKey = mode

		return nil
	}
}

// Compile parses text as a Go template. The error is text/template's, such as "template: body:1: unclosed action",
// or names an environment variable env is given and the engine doesn't allow.
func (e *GoTemplateEngine) Compile(name, text string) (Template, error) {
	if e.missingKey == "" {
		return nil, errors.New("vlatte: go template engine: create it with NewGoTemplateEngine")
	}

	t, err := template.New(name).Option("missingkey=" + e.missingKey).Funcs(e.funcs).Parse(text)
	if err != nil {
		return nil, err
	}

	if !e.customEnv {
		if err := e.env.Check(t); err != nil {
			return nil, fmt.Errorf("template: %s: env: %w", name, err)
		}
	}

	return t, nil
}

// checkFuncs returns the problem text/template would panic with when given funcs: a name a template can't call,
// or a value that isn't a function it can call.
func checkFuncs(funcs map[string]any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("template functions: %v", r)
		}
	}()

	template.New("").Funcs(funcs)

	return nil
}
