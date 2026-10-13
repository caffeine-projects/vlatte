// Package webhook provides functionality for configuring and working with webhooks.
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
		w.Transform = transform
		return nil
	}
}

// WithRoundTripper returns an [Option] that sets the [http.RoundTripper] for a [WebHook].
// This allows customization of how HTTP requests are executed.
func WithRoundTripper(roundTripper http.RoundTripper) Option {
	return func(w *WebHook) error {
		w.RoundTripper = roundTripper
		return nil
	}
}

// WithTimeout returns an [Option] that limits how long a [WebHook] request may take, from sending it to its answer.
// Defaults to 10 seconds, which 0 keeps. A negative timeout is an error.
func WithTimeout(timeout time.Duration) Option {
	return func(w *WebHook) error {
		if timeout < 0 {
			return fmt.Errorf("negative timeout %s", timeout)
		}

		w.Timeout = timeout

		return nil
	}
}

// WithLogger returns an [Option] that sets the logger of a [WebHook]'s sends and answers. Without it, a WebHook
// logs with the logger of its event: mockhttp gives the server's, so Setup().Silent() silences webhooks too.
func WithLogger(logger *zerolog.Logger) Option {
	return func(w *WebHook) error {
		w.Logger = logger
		return nil
	}
}
