package vlatte

import "io"

// TemplateEngine compiles templates: the bodies, headers and body file names of replies, the parts of multipart
// bodies, and the parameters of events, such as webhooks. A server compiles all of them with one engine.
//
// Templates compile when a mock is built, so one that doesn't compile fails the mock rather than a request. A body
// file whose content is a template compiles when a request reads it, since its name can depend on the request, so
// Compile must be safe for concurrent use.
//
// GoTemplateEngine is the engine vlatte ships, and the default. Another engine, such as one for Handlebars, plugs in
// by implementing this interface.
type TemplateEngine interface {
	// Compile parses text. name says which template it is in errors, such as "body" or "header.X-Id".
	Compile(name, text string) (Template, error)
}

// Template is a compiled template.
type Template interface {
	// Execute writes the template applied to data to w. A mock executes its templates for every request it serves,
	// so Execute must be safe for concurrent use.
	Execute(w io.Writer, data any) error
}
