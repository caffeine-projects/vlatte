package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/configutil"
	"github.com/rs/zerolog"
)

var _ vlatte.EventListener[vlatte.OnComplete] = (*WebHook)(nil)

var ErrBadArg = errors.New("webhook: bad argument")
var ErrRenderingTemplate = errors.New("webhook: rendering error")

type Option func(*WebHook) error

// Transform lets users transform and customize the [WebHook] event args and the [Args]
// before they are used. It runs every time [WebHook.Run] is called.
type Transform func(ctx context.Context, args vlatte.OnComplete, webhookArgs *Args) error

// WebHook is an automated HTTP request sent when an on-complete event is fired.
// Register it with [vlatte.Events.OnComplete] (e.g. via xhttp.Setup().OnComplete(name, wh)).
type WebHook struct {
	RoundTripper http.RoundTripper
	Transform    Transform
	Logger       *zerolog.Logger
	LogLevel     zerolog.Level
}

// New creates a new WebHook instance.
func New(options ...Option) (*WebHook, error) {
	w := &WebHook{LogLevel: zerolog.Disabled}

	for i, opt := range options {
		if err := opt(w); err != nil {
			return nil, fmt.Errorf("webhook: error applying option at index %d. reason: %w", i, err)
		}
	}

	if w.RoundTripper == nil {
		w.RoundTripper = &http.Transport{}
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
		return fmt.Errorf("webhook: arguments must not be nil")
	}

	rawArgs := args.Parameters()
	webhookArgs, ok := rawArgs.(*Args)
	if !ok {
		if m, ok := rawArgs.(map[string]any); ok {
			var err error
			if webhookArgs, err = ArgsFromMap(m); err != nil {
				return fmt.Errorf("webhook: error decoding args: %w", err)
			}
		} else {
			return fmt.Errorf("webhook: arguments must be *Args or map[string]any")
		}
	}

	if err := w.Transform(ctx, args, webhookArgs); err != nil {
		return fmt.Errorf("webhook: failed to Transform args: %w", err)
	}

	method := webhookArgs.Method
	if len(method) == 0 {
		method = http.MethodGet
	}

	u, err := url.Parse(webhookArgs.URL)
	if err != nil {
		return fmt.Errorf("webhook: error parsing %s: %w", webhookArgs.URL, err)
	}

	var body io.Reader
	if len(webhookArgs.Body) > 0 {
		body = strings.NewReader(webhookArgs.Body)
	}

	txtURL := u.String()
	w.Logger.Info().
		Str("url", txtURL).
		Str("kind", args.Kind().String()).
		Msg(fmt.Sprintf("---> WEBHOOK REQUEST RECEIVED %s %s", method, u.Path))

	req, err := http.NewRequestWithContext(ctx, method, txtURL, body)
	if err != nil {
		return err
	}

	for k, v := range webhookArgs.Header {
		req.Header.Set(k, v)
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
type Args struct {
	URL            string
	URLTemplate    string
	Method         string
	Header         map[string]string
	HeaderTemplate map[string]string
	Body           string
	BodyFile       string
	LogLevel       int
}

func ArgsFromMap(m map[string]any) (*Args, error) {
	var r = configutil.New(m)
	var err error

	if err = r.Either("url", "url_template"); err != nil {
		return nil, errorBadArg(err)
	}

	if err = r.OneOf("body", "body_file", "body_template"); err != nil {
		return nil, errorBadArg(err)
	}

	args := new(Args)
	args.URL = r.Str("url", "")
	args.Method = r.Str("method", "")
	args.Header = r.StrMap("header")
	args.HeaderTemplate = r.StrMap("header_template")
	args.Body = r.Str("body", "")
	args.BodyFile = r.Str("body_file", "")
	args.LogLevel = r.Int("log_level", int(zerolog.ErrorLevel))

	return args, nil
}

func errorBadArg(cause error) error {
	return fmt.Errorf("%w: %w", ErrBadArg, cause)
}
