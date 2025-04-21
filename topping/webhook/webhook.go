package webhook

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"

	"github.com/caffeine-projects/vlatte/std"
)

var _ std.PostAction = (*WebHook)(nil)

type Option func(*WebHook) error

// Transform lets users transforms and customize the [WebHook] [*std.PostActionInput] and the [Args]
// before they are used.
// It will be executed everytime [WebHook] runs.
type Transform func(ctx context.Context, input std.PostActionInput, args *Args) error

// WebHook is an automated HTTP request sent after a mocked response is served.
type WebHook struct {
	RoundTripper http.RoundTripper
	Transform    Transform
	Logger       *slog.Logger
	LogLevel     slog.Level
}

// New creates a new WebHook instance.
// New should be used to register the WebHook [std.PostAction] within the server instance.
func New(options ...Option) (*WebHook, error) {
	w := &WebHook{Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))}

	for i, opt := range options {
		if err := opt(w); err != nil {
			return nil, fmt.Errorf("webhook: error applying option at index %d. reason: %w", i, err)
		}
	}

	if w.RoundTripper == nil {
		w.RoundTripper = &http.Transport{}
	}

	return w, nil
}

func (w *WebHook) Name() string {
	return "webhook"
}

// Run calls parses the parameters and calls the configured service.
func (w *WebHook) Run(ctx context.Context, input std.PostActionInput) error {
	if input.Args() == nil {
		return fmt.Errorf("webhook: arguments must not be nil")
	}

	args, ok := input.Args().(Args)
	if !ok {
		err := mapstructure.Decode(input.Args(), &args)
		if err != nil {
			return fmt.Errorf("webhook: error decoding args:\n%w", err)
		}
	}

	if w.Transform != nil {
		if err := w.Transform(ctx, input, &args); err != nil {
			return fmt.Errorf("webhook: failed to Transform args: %w", err)
		}
	}

	method := args.Method
	if len(method) == 0 {
		method = http.MethodGet
	}

	u, err := url.Parse(args.URL)
	if err != nil {
		return fmt.Errorf("webhook: error parsing %s: %w", args.URL, err)
	}

	var body io.Reader
	if len(args.Body) > 0 {
		body = strings.NewReader(args.Body)
	}

	txtURL := u.String()
	w.Logger.Info(fmt.Sprintf("---> WEBHOOK REQUEST RECEIVED %s %s", method, u.Path),
		slog.String("url", txtURL),
		slog.String("kind", input.Kind()))

	req, err := http.NewRequestWithContext(ctx, method, txtURL, body)
	if err != nil {
		return err
	}

	for k, v := range args.Header {
		req.Header.Add(k, v)
	}

	res, err := w.RoundTripper.RoundTrip(req)
	if err != nil {
		w.Logger.Error(fmt.Sprintf("<--- WEBHOOK: %s %s", method, u.Path),
			slog.String("url", txtURL),
			slog.String("kind", input.Kind()))
		return fmt.Errorf("webhook: request failed: %w", err)
	}

	defer res.Body.Close()

	level := slog.LevelInfo
	if res.StatusCode >= http.StatusBadRequest {
		level = slog.LevelError
	}

	w.Logger.Log(ctx, level, fmt.Sprintf("<--- WEBHOOK %s %s", method, u.Path),
		slog.String("url", txtURL),
		slog.Int("status", res.StatusCode),
		slog.String("kind", input.Kind()))

	return nil
}

// Args configures an WebHook HTTP request.
// It will be parsed from the parameters map provided during mocking phase.
type Args struct {
	URL            string            `mapstructure:"url"`
	Method         string            `mapstructure:"method"`
	Header         map[string]string `mapstructure:"header"`
	HeaderTemplate map[string]string `mapstructure:"header_template"`
	Body           string            `mapstructure:"body"`
	BodyFile       string            `mapstructure:"body_file"`
	IsTemplate     bool              `mapstructure:"is_template"`
	LogLevel       int               `mapstructure:"log_level"`
}
