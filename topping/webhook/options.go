// Package webhook provides functionality for configuring and working with webhooks.
package webhook

import (
	"log/slog"
	"net/http"
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

// WithLogger returns an [Option] that sets the logger for a [WebHook].
// The logger is used for recording webhook activities and errors.
func WithLogger(logger *slog.Logger) Option {
	return func(w *WebHook) error {
		w.Logger = logger
		return nil
	}
}

// WithLogLevel returns an [Option] that sets the logging level for a [WebHook].
// This controls the verbosity of webhook-related logs.
func WithLogLevel(level slog.Level) Option {
	return func(w *WebHook) error {
		w.LogLevel = level
		return nil
	}
}
