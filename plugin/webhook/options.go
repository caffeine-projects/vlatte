// Package webhook provides functionality for configuring and working with webhooks.
package webhook

import (
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

// WithTimeout returns an [Option] that limits how long a [WebHook] request may take.
// Defaults to 10 seconds.
func WithTimeout(timeout time.Duration) Option {
	return func(w *WebHook) error {
		w.Timeout = timeout
		return nil
	}
}

// WithLogger returns an [Option] that sets the logger for a [WebHook].
// The logger is used for recording webhook activities and errors.
func WithLogger(logger *zerolog.Logger) Option {
	return func(w *WebHook) error {
		w.Logger = logger
		return nil
	}
}

// WithLogLevel returns an [Option] that sets the logging level for a [WebHook].
// This controls the verbosity of webhook-related logs.
func WithLogLevel(level zerolog.Level) Option {
	return func(w *WebHook) error {
		w.LogLevel = level
		return nil
	}
}
