// Package webhook sends webhooks: HTTP requests a mock makes after it replies. A WebHook is an on_complete listener:
// register it under the name mocks give, as with mockhttp.Setup().OnCompleteListener("webhook", wh). The vlatte
// command registers one as webhook. Its parameters are Args, or a map as mock files write them: see ArgsFromMap.
package webhook

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// WithTransform returns an [Option] that sets the [Transform] function for a [WebHook].
// The Transform function allows customization of request data before sending.
func WithTransform(transform Transform) Option {
	return func(w *WebHook) error {
		w.transform = transform
		return nil
	}
}

// WithTransport returns an [Option] that sets the [http.RoundTripper] for a [WebHook], in place of the server's,
// which the event's [vlatte.Kit] gives.
func WithTransport(transport http.RoundTripper) Option {
	return func(w *WebHook) error {
		w.transport = transport
		return nil
	}
}

// WithTimeout returns an [Option] that limits how long a [WebHook] request may take, from sending it to its answer.
// It wins over the event's, such as the server's Config.WebhookTimeout mockhttp gives. 0 keeps the event's, or else
// 10 seconds. A negative timeout is an error.
func WithTimeout(timeout time.Duration) Option {
	return func(w *WebHook) error {
		if timeout < 0 {
			return fmt.Errorf("negative timeout %s", timeout)
		}

		w.timeout = timeout

		return nil
	}
}

// WithLogger returns an [Option] that sets the logger of a [WebHook]'s sends and answers. Without it, a WebHook
// logs with the server's logger, which the event's [vlatte.Kit] gives, so Setup().Silent() silences webhooks too.
func WithLogger(logger *zerolog.Logger) Option {
	return func(w *WebHook) error {
		w.logger = logger
		return nil
	}
}
