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

// Body sets the HTTP request body of the WebHook.
func (b *ArgsBuilder) Body(body string) *ArgsBuilder {
	b.input.Body = body
	return b
}

// Delay sets how long the WebHook waits, once the mock replied, before it sends the request. The response waits for
// it too.
func (b *ArgsBuilder) Delay(d time.Duration) *ArgsBuilder {
	b.input.Delay = d
	return b
}

// Multipart sets a multipart body for the WebHook, such as one built with mockhttp.MultipartForm.
func (b *ArgsBuilder) Multipart(body MultipartBody) *ArgsBuilder {
	b.input.Multipart = body
	return b
}

// Build builds the vlatte.EventConfig required to set up a WebHook for a mock (on-complete event). The config gets a
// copy of the parameters, so changing the builder afterwards changes only the configs built afterwards.
func (b *ArgsBuilder) Build(name string) vlatte.EventConfig {
	return vlatte.EventConfig{Name: name, Parameters: b.input.clone()}
}
