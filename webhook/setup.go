package webhook

import (
	"time"

	"github.com/caffeine-projects/vlatte"
)

// ArgsBuilder provides a convenient way to configure a WebHook for a mock.
type ArgsBuilder struct {
	input *Args
}

// Options starts an ArgsBuilder to configure a WebHook for a mock.
func Options() *ArgsBuilder {
	return &ArgsBuilder{input: &Args{Header: make(map[string]string)}}
}

// URL sets the URL of the target service.
func (b *ArgsBuilder) URL(u string) *ArgsBuilder {
	b.input.URL = u
	return b
}

// URLTemplate sets the URL of the target service as a template, rendered with the request that completed the mock.
func (b *ArgsBuilder) URLTemplate(t string) *ArgsBuilder {
	b.input.URLTemplate = t
	return b
}

// Method sets the HTTP method.
func (b *ArgsBuilder) Method(method string) *ArgsBuilder {
	b.input.Method = method
	return b
}

// Header adds an HTTP header to be sent in the WebHook.
func (b *ArgsBuilder) Header(k, v string) *ArgsBuilder {
	b.input.Header[k] = v
	return b
}

// HeaderTemplate adds an HTTP header whose value is a template, rendered with the request that completed the mock.
func (b *ArgsBuilder) HeaderTemplate(k, v string) *ArgsBuilder {
	if b.input.HeaderTemplate == nil {
		b.input.HeaderTemplate = make(map[string]string)
	}

	b.input.HeaderTemplate[k] = v

	return b
}

// Body sets the HTTP request body of the WebHook.
func (b *ArgsBuilder) Body(body string) *ArgsBuilder {
	b.input.Body = body
	return b
}

// BodyTemplate sets the HTTP request body of the WebHook as a template, rendered with the request that completed the
// mock.
func (b *ArgsBuilder) BodyTemplate(t string) *ArgsBuilder {
	b.input.BodyTemplate = t
	return b
}

// BodyFile sets the file the HTTP request body of the WebHook is read from, each time it is sent. A relative name opens
// from the working directory.
func (b *ArgsBuilder) BodyFile(name string) *ArgsBuilder {
	b.input.BodyFile = name
	return b
}

// Delay sets how long the WebHook waits, once the mock replied, before it sends the request. The response waits for
// it too.
func (b *ArgsBuilder) Delay(d time.Duration) *ArgsBuilder {
	b.input.Delay = d
	return b
}

// Timeout limits how long the request of the WebHook may take, from sending it to its answer. It wins over the
// listener's webhook.WithTimeout.
func (b *ArgsBuilder) Timeout(d time.Duration) *ArgsBuilder {
	b.input.Timeout = d
	return b
}

// Multipart sets a multipart body for the WebHook, such as one built with mockhttp.MultipartForm. For a body whose
// text is templates, call Template on it: mockhttp.MultipartForm(...).Template().
func (b *ArgsBuilder) Multipart(body MultipartBody) *ArgsBuilder {
	b.input.Multipart = body
	return b
}

// Build builds the vlatte.EventConfig required to set up a WebHook for a mock (on-complete event). The config gets a
// copy of the parameters, so changing the builder afterwards changes only the configs built afterwards.
func (b *ArgsBuilder) Build(name string) vlatte.EventConfig {
	return vlatte.EventConfig{Name: name, Parameters: b.input.clone()}
}
