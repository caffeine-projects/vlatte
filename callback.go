package vlatte

import "context"

// CallbackInput represents the arguments that will be passed to every [Callback] implementation.
// The actual implementation of this interface will likely expose more functionality.
// The [CallbackInput.Kind] function can be used to determine the type of the input to help cast it to a more appropriate type,
// depending on the context of the callback.
type CallbackInput interface {
	// Kind returns the type of the callback.
	// Example: if HTTP mock is being used, it will return "http".
	Kind() Kind

	// Bean is the matched [Bean] for the current request.
	Bean() Bean
}

// Callback defines the contract for a simple action that will be executed after serving a mocked response.
// Similar to event listeners ([EventListener], [OnComplete]), but it is just a function and cannot be configured through files.
type Callback func(ctx context.Context, input CallbackInput) error
