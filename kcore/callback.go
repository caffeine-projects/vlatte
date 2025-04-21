package kcore

// CallbackInput represents the arguments that will be passed to every Callback implementation
type CallbackInput struct {
	// RawRequest is the original incoming http.Request.
	RawRequest any

	// URL is the full request url.URL, including scheme, host, port.
	URL any

	// ParsedBody is the parsed http.Request body.
	ParsedBody any

	// App exposes the application instance associated with the incoming HTTP request.
	App any

	// Mock is the matched Mock for the current HTTP request.
	Mock any

	// MockedResponse is the HTTP response MockedResponse served.
	MockedResponse any
}

// Callback defines the contract for an action that will be executed after serving a mocked HTTP response.
type Callback func(input *CallbackInput) error
