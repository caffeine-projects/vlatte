package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/internal/value"
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
		if webhookArgs, err = argsFromMap(m, originOf(args)); err != nil {
			return err
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

	if bodies := countSet(webhookArgs.Body, webhookArgs.BodyTemplate, webhookArgs.BodyFile) + countSet(webhookArgs.Multipart != nil); bodies > 1 {
		return errors.New("webhook: more than one body is set: keep one of Body, BodyTemplate, BodyFile and Multipart")
	}

	var body io.Reader

	contentType := ""

	switch {
	case webhookArgs.Multipart != nil:
		ct, b, err := webhookArgs.Multipart.Encode(data)
		if err != nil {
			return fmt.Errorf("webhook: multipart body: %w", err)
		}

		body, contentType = bytes.NewReader(b), ct
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

	if contentType != "" {
		if req.Header.Get("Content-Type") != "" {
			return errors.New("webhook: a multipart body sets the Content-Type: remove the Content-Type header")
		}

		req.Header.Set("Content-Type", contentType)
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

	// Multipart is a multipart body, such as one built with mockhttp.MultipartForm. It sets the Content-Type.
	Multipart MultipartBody
}

// MultipartBody is a multipart body for a webhook, such as one built with mockhttp.MultipartForm.
type MultipartBody interface {
	// Encode writes the body, with data for its templates: the data of the request that completed the mock.
	// It returns the body's Content-Type and bytes.
	Encode(data any) (contentType string, body []byte, err error)
}

type argsField = config.Field[Args]

// _argsSchema declares the parameters of a webhook event, the way a mock file's on_complete writes them.
var _argsSchema = config.NewSchema(
	argsField{
		Key: "url", Kind: config.String, Usage: "URL to send the request to.",
		Set: func(a *Args, v config.Value) error { a.URL = v.Str; return nil },
	},
	argsField{
		Key: "url_template", Kind: config.String, Usage: "URL to send the request to, as a Go template.",
		Set: func(a *Args, v config.Value) error { a.URLTemplate = v.Str; return nil },
	},
	argsField{
		Key: "method", Kind: config.String, Usage: "Method of the request. Empty means GET.",
		Set: func(a *Args, v config.Value) error { a.Method = v.Str; return nil },
	},
	argsField{
		Key: "header", Kind: config.Any, Usage: "Headers of the request, by name.",
		Set: func(a *Args, v config.Value) error { return headerOf(v, &a.Header) },
	},
	argsField{
		Key: "header_template", Kind: config.Any, Usage: "Headers of the request whose values are Go templates, by name.",
		Set: func(a *Args, v config.Value) error { return headerOf(v, &a.HeaderTemplate) },
	},
	argsField{
		Key: "body", Kind: config.String, Usage: "Body of the request.",
		Set: func(a *Args, v config.Value) error { a.Body = v.Str; return nil },
	},
	argsField{
		Key: "body_template", Kind: config.String, Usage: "Body of the request, as a Go template.",
		Set: func(a *Args, v config.Value) error { a.BodyTemplate = v.Str; return nil },
	},
	argsField{
		Key: "body_file", Kind: config.String, Usage: "File to read the body of the request from.",
		Set: func(a *Args, v config.Value) error { a.BodyFile = v.Str; return nil },
	},
	argsField{
		Key: "multipart", Kind: config.Any, Usage: "Multipart body of the request: type, boundary, params and parts.",
		Set: func(a *Args, v config.Value) error { return setMultipart(a, v, false) },
	},
	argsField{
		Key: "multipart_template", Kind: config.Any,
		Usage: "Multipart body whose text is Go templates: names, filenames, content types, header values, text bodies and body file names.",
		Set:   func(a *Args, v config.Value) error { return setMultipart(a, v, true) },
	},
)

// _exclusive lists the sets of parameters of which only one can be set.
var _exclusive = [][]string{{"url", "url_template"}, {"body", "body_file", "body_template", "multipart", "multipart_template"}}

// ArgsFromMap decodes the parameters of a webhook event, as a mock file's on_complete writes them:
//   - url, or url_template, a Go template: a string;
//   - method: a string, GET when empty;
//   - header and header_template, whose values are Go templates: objects of strings;
//   - body, body_template, a Go template, or body_file: a string.
//
// As in mock files, each value must have its type, and other keys are errors. A map built in code means what its
// JSON means, so a map[string]string or an int is fine where its JSON is. The error wraps ErrBadArg and lists
// every problem.
func ArgsFromMap(m map[string]any) (*Args, error) {
	return argsFromMap(m, "on_complete")
}

// argsFromMap decodes the parameters m, which came from origin, such as "mock users", for problems.
func argsFromMap(m map[string]any, origin string) (*Args, error) {
	doc, err := value.ViaJSON(m)
	if err != nil {
		return nil, errorBadArg(err)
	}

	args := new(Args)
	err = _argsSchema.Decode(args, config.Value{Key: "parameters", Origin: origin, Any: doc})

	problems := conflicts(doc, origin)
	if err == nil && len(problems) == 0 {
		return args, nil
	}

	if cfgErr, ok := errors.AsType[*config.Error](err); ok {
		problems = append(cfgErr.Problems, problems...)
	}

	return nil, errorBadArg(&config.Error{Scope: "parameters", Problems: problems})
}

// conflicts reports each parameter of an exclusive set after the first one set: only one of them can be.
func conflicts(doc any, origin string) []config.Problem {
	params, _ := doc.(map[string]any)

	var problems []config.Problem
	for _, set := range _exclusive {
		first := ""
		for _, key := range set {
			if _, ok := params[key]; !ok {
				continue
			}

			if first == "" {
				first = key
				continue
			}

			problems = append(problems, config.Problem{
				Code: config.CodeConflict, Origin: origin, Key: "parameters." + key,
				Message: fmt.Sprintf("parameters.%s is set with parameters.%s", key, first), Hint: "keep one of them",
			})
		}
	}

	// A multipart body sets the Content-Type, with its boundary.
	for _, multipart := range []string{"multipart", "multipart_template"} {
		if _, ok := params[multipart]; !ok {
			continue
		}

		for _, headers := range []string{"header", "header_template"} {
			names, _ := params[headers].(map[string]any)
			for _, name := range slices.Sorted(maps.Keys(names)) {
				if strings.EqualFold(name, "Content-Type") {
					key := "parameters." + headers + "." + name
					problems = append(problems, config.Problem{
						Code: config.CodeConflict, Origin: origin, Key: key,
						Message: key + " is set with parameters." + multipart, Hint: "a multipart body sets the Content-Type: remove the header",
					})
				}
			}
		}

		break
	}

	return problems
}

// setMultipart decodes a multipart body. A webhook sends its body at once, so its parts can't have delays.
func setMultipart(a *Args, v config.Value, templated bool) error {
	spec, err := mpart.Decode(v)
	if spec == nil {
		return err
	}

	var problems []config.Problem
	if cfgErr, ok := errors.AsType[*config.Error](err); ok {
		problems = cfgErr.Problems
	} else if err != nil {
		return err
	}

	for i, p := range spec.Parts {
		if p.Delay > 0 {
			key := fmt.Sprintf("%s.parts[%d].delay", v.Key, i)
			problems = append(problems, config.Problem{
				Code: config.CodeNotAllowed, Origin: v.Origin, Key: key,
				Message: key + " can't be set", Hint: "a webhook sends its body at once",
			})
		}
	}

	if len(problems) > 0 {
		return &config.Error{Problems: problems}
	}

	spec.Templated = templated
	a.Multipart = &multipartParams{spec: spec}

	return nil
}

// multipartParams is a multipart body of a webhook's parameters.
type multipartParams struct {
	spec *mpart.Spec
}

// Encode implements MultipartBody.
func (m *multipartParams) Encode(data any) (string, []byte, error) {
	e, err := mpart.Compile(m.spec)
	if err != nil {
		return "", nil, err
	}

	b, _, err := e.Encode(data)

	return e.ContentType(), b, err
}

// countSet counts the values that are set: non-empty strings and true bools.
func countSet[T string | bool](values ...T) int {
	var zero T

	n := 0
	for _, v := range values {
		if v != zero {
			n++
		}
	}

	return n
}

// headerOf decodes an object of header values, each a string.
func headerOf(v config.Value, dst *map[string]string) error {
	header := make(map[string]string)
	err := config.Members(v, func(name string, member config.Value) error {
		s, ok := member.Any.(string)
		if !ok {
			return config.Mismatch(member, "a string", config.String)
		}

		header[name] = s

		return nil
	})

	*dst = header

	return err
}

// originOf names where the parameters of an event come from, for problems: the mock that completed.
func originOf(args vlatte.OnComplete) string {
	if mock := args.Mock(); mock != nil {
		return "mock " + mock.ID()
	}

	return "on_complete"
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
