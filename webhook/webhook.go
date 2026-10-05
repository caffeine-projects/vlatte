package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/configutil"
)

var _ vlatte.EventListener[vlatte.OnComplete] = (*WebHook)(nil)

var (
	ErrBadArg            = errors.New("webhook: bad argument")
	ErrRenderingTemplate = errors.New("webhook: rendering error")
)

type Option func(*WebHook) error

// Transform lets users transform and customize the [WebHook] event args and the [Args]
// before they are used. It runs every time [WebHook.Run] is called.
type Transform func(ctx context.Context, args vlatte.OnComplete, webhookArgs *Args) error

// WebHook is an automated HTTP request sent when an on-complete event is fired.
// Register it with [vlatte.Listener.OnComplete] (e.g. via mockhttp.Setup().OnCompleteListener(name, wh)).
type WebHook struct {
	RoundTripper http.RoundTripper
	Transform    Transform
	Logger       *zerolog.Logger
	LogLevel     zerolog.Level
	Timeout      time.Duration
}

const _defaultTimeout = 10 * time.Second

// New creates a new WebHook instance.
func New(options ...Option) (*WebHook, error) {
	w := &WebHook{LogLevel: zerolog.Disabled}

	for i, opt := range options {
		if err := opt(w); err != nil {
			return nil, fmt.Errorf("webhook: error applying option at index %d. reason: %w", i, err)
		}
	}

	if w.RoundTripper == nil {
		// A clone keeps the default dial, TLS handshake and idle timeouts.
		// If another package replaced the default transport with a different type, share it as is.
		w.RoundTripper = http.DefaultTransport
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			w.RoundTripper = t.Clone()
		}
	}

	if w.Timeout == 0 {
		w.Timeout = _defaultTimeout
	}

	if w.Transform == nil {
		w.Transform = func(ctx context.Context, args vlatte.OnComplete, webhookArgs *Args) error { return nil }
	}

	if w.Logger == nil {
		var wio io.Writer
		if w.LogLevel == zerolog.Disabled {
			wio = io.Discard
		} else {
			wio = os.Stdout
		}

		l := zerolog.New(wio).Level(w.LogLevel)
		w.Logger = &l
	}

	return w, nil
}

// Run implements vlatte.EventListener[vlatte.OnComplete]. It parses the event parameters and performs the HTTP request.
func (w *WebHook) Run(ctx context.Context, args vlatte.OnComplete) error {
	if args.Parameters() == nil {
		return errors.New("webhook: arguments must not be nil")
	}

	rawArgs := args.Parameters()
	webhookArgs, ok := rawArgs.(*Args)
	if !ok {
		m, ok := rawArgs.(map[string]any)
		if !ok {
			return errors.New("webhook: arguments must be *Args or map[string]any")
		}

		var err error
		if webhookArgs, err = ArgsFromMap(m); err != nil {
			return fmt.Errorf("webhook: error decoding args: %w", err)
		}
	}

	if err := w.Transform(ctx, args, webhookArgs); err != nil {
		return fmt.Errorf("webhook: failed to Transform args: %w", err)
	}

	method := webhookArgs.Method
	if len(method) == 0 {
		method = http.MethodGet
	}

	// Templates render with the data of the request that completed the mock.
	var data any
	if kit := args.Kit(); kit != nil {
		data = kit.Data()
	}

	rawURL := webhookArgs.URL
	if webhookArgs.URLTemplate != "" {
		var err error
		if rawURL, err = render("url_template", webhookArgs.URLTemplate, data); err != nil {
			return err
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("webhook: error parsing %s: %w", rawURL, err)
	}

	var body io.Reader
	switch {
	case webhookArgs.BodyTemplate != "":
		b, err := render("body_template", webhookArgs.BodyTemplate, data)
		if err != nil {
			return err
		}

		body = strings.NewReader(b)
	case webhookArgs.BodyFile != "":
		b, err := os.ReadFile(webhookArgs.BodyFile)
		if err != nil {
			return fmt.Errorf("webhook: error reading body_file %s: %w", webhookArgs.BodyFile, err)
		}

		body = bytes.NewReader(b)
	case len(webhookArgs.Body) > 0:
		body = strings.NewReader(webhookArgs.Body)
	}

	txtURL := u.String()
	w.Logger.Info().
		Str("url", txtURL).
		Str("kind", args.Kind().String()).
		Msg(fmt.Sprintf("---> WEBHOOK REQUEST RECEIVED %s %s", method, u.Path))

	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, txtURL, body)
	if err != nil {
		return err
	}

	for k, v := range webhookArgs.Header {
		req.Header.Set(k, v)
	}

	for k, v := range webhookArgs.HeaderTemplate {
		rendered, err := render("header_template "+k, v, data)
		if err != nil {
			return err
		}

		req.Header.Set(k, rendered)
	}

	res, err := w.RoundTripper.RoundTrip(req)
	if err != nil {
		w.Logger.Err(err).
			Str("url", txtURL).
			Str("kind", args.Kind().String()).
			Msg(fmt.Sprintf("<--- WEBHOOK: %s %s", method, u.Path))

		return fmt.Errorf("webhook: request failed: %w", err)
	}

	defer res.Body.Close()

	msg := fmt.Sprintf("<--- WEBHOOK %s %s", method, u.Path)
	child := w.Logger.With().
		Str("url", txtURL).
		Str("kind", args.Kind().String()).
		Int("status", res.StatusCode).
		Logger()

	if res.StatusCode >= http.StatusBadRequest {
		child.Warn().Msg(msg)
		return nil
	}

	child.Info().Msg(msg)

	return nil
}

// Args configures a WebHook HTTP request.
// It is parsed from the parameters in the event config (e.g. mock definition).
// URLTemplate, the HeaderTemplate values and BodyTemplate are Go templates,
// rendered with the data of the request that completed the mock.
type Args struct {
	URL            string
	URLTemplate    string
	Method         string
	Header         map[string]string
	HeaderTemplate map[string]string
	Body           string
	BodyTemplate   string
	BodyFile       string
	LogLevel       int
}

func ArgsFromMap(m map[string]any) (*Args, error) {
	r := configutil.New(m)
	var err error

	if err = r.Either("url", "url_template"); err != nil {
		return nil, errorBadArg(err)
	}

	if err = r.OneOf("body", "body_file", "body_template"); err != nil {
		return nil, errorBadArg(err)
	}

	args := new(Args)
	args.URL = r.Str("url", "")
	args.URLTemplate = r.Str("url_template", "")
	args.Method = r.Str("method", "")
	args.Header = r.StrMap("header")
	args.HeaderTemplate = r.StrMap("header_template")
	args.Body = r.Str("body", "")
	args.BodyTemplate = r.Str("body_template", "")
	args.BodyFile = r.Str("body_file", "")
	args.LogLevel = r.Int("log_level", int(zerolog.ErrorLevel))

	return args, nil
}

// render executes text as a Go template with the given data.
func render(name, text string, data any) (string, error) {
	t, err := template.New(name).Parse(text)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRenderingTemplate, err)
	}

	var b strings.Builder
	if err = t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("%w: %w", ErrRenderingTemplate, err)
	}

	return b.String(), nil
}

func errorBadArg(cause error) error {
	return fmt.Errorf("%w: %w", ErrBadArg, cause)
}
