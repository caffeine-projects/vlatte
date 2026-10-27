package vlatte

import (
	"errors"
	"fmt"
	"io"
)

// ErrTemplateDataKeyExists says two template data providers of a server have the same key.
var ErrTemplateDataKeyExists = errors.New("server: two template data providers have the same key")

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

// TemplateDataProvider adds data to the templates of a server's mocks, which read it as .Provided.<Key>. In is what
// the server gives it for each request it serves, such as a mockhttp.TemplateDataProviderInput.
type TemplateDataProvider[In any] interface {
	Key() string
	Data(in In) (any, error)
}

// CheckTemplateDataProviders returns ErrTemplateDataKeyExists when two providers have the same key: templates could
// read only one of them.
func CheckTemplateDataProviders[In any](providers []TemplateDataProvider[In]) error {
	keys := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		if _, ok := keys[p.Key()]; ok {
			return fmt.Errorf("%w: %q", ErrTemplateDataKeyExists, p.Key())
		}

		keys[p.Key()] = struct{}{}
	}

	return nil
}

// ProvideTemplateData returns the data providers give for in, by their key. It returns the error of the first that
// fails, which names it.
func ProvideTemplateData[In any](providers []TemplateDataProvider[In], in In) (map[string]any, error) {
	data := make(map[string]any, len(providers))

	for _, p := range providers {
		d, err := p.Data(in)
		if err != nil {
			return nil, fmt.Errorf("template data provider %s: %w", p.Key(), err)
		}

		data[p.Key()] = d
	}

	return data, nil
}
