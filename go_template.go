package vlatte

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"
)

var _ TemplateEngine = (*GoTemplateEngine)(nil)

// _missingKeyModes are the missingkey options of text/template.
var _missingKeyModes = []string{"default", "invalid", "zero", "error"}

// GoTemplateEngine compiles Go templates, of package text/template. Servers use it when no other engine is set.
// It is safe for concurrent use. Create one with NewGoTemplateEngine.
//
// text/template, and not html/template, is the package for mocks: templates write bodies of any type, such as JSON
// and XML, and headers, URLs and file names, which html/template would escape for HTML. Escape on purpose with the
// html, js and urlquery functions, or with toJson for a JSON value.
//
// Besides the functions of text/template, templates can call:
//   - toJson: a value as JSON, such as a quoted and escaped string;
//   - fromJson: JSON text as a value, such as the fields of a JSON request body;
//   - uuid: a random UUID;
//   - now: the current time.Time;
//   - base64Encode and base64Decode: a string to and from standard base64.
//
// A missing map key is an error, so a typo doesn't render as "<no value>". Read an optional key with index, which
// gives the zero value, as in {{ with index .Extra "name" }}{{ . }}{{ end }}, or change the rule with WithMissingKey.
type GoTemplateEngine struct {
	funcs      map[string]any
	missingKey string
}

// GoTemplateOption configures a GoTemplateEngine.
type GoTemplateOption func(*GoTemplateEngine) error

// NewGoTemplateEngine creates a GoTemplateEngine.
func NewGoTemplateEngine(opts ...GoTemplateOption) (*GoTemplateEngine, error) {
	e := &GoTemplateEngine{funcs: goTemplateFuncs(), missingKey: "error"}

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

// WithTemplateFuncs adds functions templates can call, by name. A function replaces a built-in one of the same
// name. Each must be a function text/template can call: one result, or two with an error second.
func WithTemplateFuncs(funcs map[string]any) GoTemplateOption {
	return func(e *GoTemplateEngine) error {
		if err := checkFuncs(funcs); err != nil {
			return err
		}

		maps.Copy(e.funcs, funcs)

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

// Compile parses text as a Go template. The error is text/template's, such as "template: body:1: unclosed action".
func (e *GoTemplateEngine) Compile(name, text string) (Template, error) {
	if e.missingKey == "" {
		return nil, errors.New("vlatte: go template engine: create it with NewGoTemplateEngine")
	}

	t, err := template.New(name).Option("missingkey=" + e.missingKey).Funcs(e.funcs).Parse(text)
	if err != nil {
		return nil, err
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
