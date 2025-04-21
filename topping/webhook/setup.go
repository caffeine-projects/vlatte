package webhook

import "github.com/caffeine-projects/vlatte/std"

// builder provides a convenient way to configure a WebHook for a mock.
type builder struct {
	input *Args
}

// Options creates a new Builder to configure a WebHook for a mock.
func Options() *builder {
	return &builder{input: &Args{Header: make(map[string]string)}}
}

// URL sets the URL of the target service.
func (b *builder) URL(u string) *builder {
	b.input.URL = u
	return b
}

// Method sets the HTTP method.
func (b *builder) Method(method string) *builder {
	b.input.Method = method
	return b
}

// Header adds an HTTP header to be sent in the WebHook.
func (b *builder) Header(k, v string) *builder {
	b.input.Header[k] = v
	return b
}

// Body sets the HTTP request body of the WebHook.
func (b *builder) Body(body string) *builder {
	b.input.Body = body
	return b
}

// Build builds the vlatte.PostActionDef required to set up a WebHook for a mock.
func (b *builder) Build(name string) std.PostActionDefinition {
	return std.PostActionDefinition{Name: name, Parameters: b.input}
}
