package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.EventListener[vlatte.OnComplete] = (*WebHook)(nil)
	_ vlatte.EventPreparer                    = (*WebHook)(nil)
)

var (
	ErrBadArg            = errors.New("webhook: bad argument")
	ErrRenderingTemplate = errors.New("webhook: rendering error")
)

// _defaultEngine compiles the templates of a webhook run without a Kit, such as one called directly.
var _defaultEngine = vlatte.MustNewGoTemplateEngine()

type Option func(*WebHook) error

// Transform lets users transform and customize the [WebHook] event args and the [Args]
// before they are used. It runs every time [WebHook.Run] is called.
type Transform func(ctx context.Context, args vlatte.OnComplete, webhookArgs *Args) error

// WebHook is an automated HTTP request sent when an on-complete event is fired.
// Register it with [vlatte.Listener.OnComplete] (e.g. via mockhttp.Setup().OnCompleteListener(name, wh)).
//
// It logs each send and its answer at info level, with Logger, or else with the logger of the event: mockhttp gives
// the server's. A send fails when the target can't be reached in Timeout, or answers with a status outside 2xx; a
// redirect isn't followed. A zero WebHook sends with http.DefaultTransport and a 10s timeout.
type WebHook struct {
	RoundTripper http.RoundTripper
	Transform    Transform
	Logger       *zerolog.Logger
	Timeout      time.Duration
}

const _defaultTimeout = 10 * time.Second

// New creates a new WebHook instance.
func New(options ...Option) (*WebHook, error) {
	w := &WebHook{}

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

	return w, nil
}

// Prepare implements vlatte.EventPreparer. It decodes the parameters of a mock's webhook, checks them, and compiles
// their templates with the server's engine, so a mistake fails the mock when it is built instead of each send. Run
// receives the decoded *Args.
func (w *WebHook) Prepare(cfg vlatte.EventConfig, engine vlatte.TemplateEngine) (vlatte.Parameters, error) {
	args, err := argsOf(cfg.Parameters, "on_complete", cfg.Dir)
	if err != nil {
		return nil, err
	}

	if err := args.validate(); err != nil {
		return nil, err
	}

	if err := args.check(engine); err != nil {
		return nil, err
	}

	return args, nil
}

// Run implements vlatte.EventListener[vlatte.OnComplete]. It parses the event parameters and performs the HTTP request.
func (w *WebHook) Run(ctx context.Context, args vlatte.OnComplete) error {
	// Each run has its own copy of the parameters, so a Transform changes only this request.
	webhookArgs, err := argsOf(args.Parameters(), originOf(args), dirOf(args))
	if err != nil {
		return err
	}

	// A Transform can add headers, whether the parameters have some or not.
	if webhookArgs.Header == nil {
		webhookArgs.Header = make(map[string]string)
	}

	if webhookArgs.HeaderTemplate == nil {
		webhookArgs.HeaderTemplate = make(map[string]string)
	}

	if w.Transform != nil {
		if err := w.Transform(ctx, args, webhookArgs); err != nil {
			return fmt.Errorf("webhook: failed to Transform args: %w", err)
		}
	}

	// The parameters were checked when the mock was built; a Transform may have changed them since.
	if err := webhookArgs.validate(); err != nil {
		return err
	}

	// Request methods match in any case, as in mock files: method: post sends POST.
	method := strings.ToUpper(webhookArgs.Method)
	if len(method) == 0 {
		method = http.MethodGet
	}

	engine, data := templating(args)

	rawURL := webhookArgs.URL
	if webhookArgs.URLTemplate != "" {
		var err error
		if rawURL, err = render(engine, "url_template", webhookArgs.URLTemplate, data); err != nil {
			return err
		}

		if err := checkURL(rawURL); err != nil {
			return fmt.Errorf("webhook: url_template: %w", err)
		}
	}

	// validate and checkURL parsed it already.
	u, _ := url.Parse(rawURL)

	var body io.Reader

	contentType := ""

	switch {
	case webhookArgs.Multipart != nil:
		ct, b, err := webhookArgs.Multipart.Encode(engine, data)
		if err != nil {
			return fmt.Errorf("webhook: multipart body: %w", err)
		}

		body, contentType = bytes.NewReader(b), ct
	case webhookArgs.BodyTemplate != "":
		b, err := render(engine, "body_template", webhookArgs.BodyTemplate, data)
		if err != nil {
			return err
		}

		body = strings.NewReader(b)
	case webhookArgs.BodyFile != "":
		name := webhookArgs.BodyFile
		if webhookArgs.dir != "" && !filepath.IsAbs(name) {
			name = filepath.Join(webhookArgs.dir, name)
		}

		b, err := os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("webhook: error reading body_file %s: %w", webhookArgs.BodyFile, err)
		}

		body = bytes.NewReader(b)
	case len(webhookArgs.Body) > 0:
		body = strings.NewReader(webhookArgs.Body)
	}

	logger := w.Logger
	if logger == nil {
		logger = loggerOf(args)
	}

	// The URL may hold a password: logs and errors show it redacted.
	redacted := u.Redacted()
	logger.Info().
		Str("url", redacted).
		Str("kind", args.Kind().String()).
		Msgf("---> WEBHOOK REQUEST %s %s", method, u.Path)

	timeout := w.Timeout
	if timeout <= 0 {
		timeout = _defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}

	for k, v := range webhookArgs.Header {
		req.Header.Set(k, v)
	}

	for k, v := range webhookArgs.HeaderTemplate {
		rendered, err := render(engine, "header_template "+k, v, data)
		if err != nil {
			return err
		}

		// As in replies: the value goes without the spaces and line breaks around it, and a line break inside, which
		// would end the header there, fails here, whatever the RoundTripper would do with it.
		rendered = textproto.TrimString(rendered)
		if strings.ContainsAny(rendered, "\r\n") {
			return fmt.Errorf("webhook: header_template %s holds a line break", k)
		}

		req.Header.Set(k, rendered)
	}

	// validate made sure no header sets it.
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// As http.Client does: the user and password of the URL go as Basic auth, unless a header sets Authorization.
	if u.User != nil && req.Header.Get("Authorization") == "" {
		password, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), password)
	}

	roundTripper := w.RoundTripper
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}

	// The caller logs the errors: logging them here too would log each twice.
	res, err := roundTripper.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("webhook: %s %s: request failed: %w", method, redacted, err)
	}

	defer res.Body.Close()

	// A redirect isn't followed, as a POST would turn into a GET: like an error, it's an answer the target must fix.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("webhook: %s %s: the target answered %s", method, redacted, res.Status)
	}

	logger.Info().
		Str("url", redacted).
		Str("kind", args.Kind().String()).
		Int("status", res.StatusCode).
		Msgf("<--- WEBHOOK %s %s", method, u.Path)

	return nil
}

// Args configures a WebHook HTTP request.
// It is parsed from the parameters in the event config (e.g. mock definition).
// URLTemplate, the HeaderTemplate values and BodyTemplate are templates, compiled with the template engine of the
// server and rendered with the data of the request that completed the mock.
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

	dir string // where relative files open from: the mock file's directory; "" is the working directory
}

// clone returns a copy of a, with copies of its maps. Multipart is shared: writing a body doesn't change it.
func (a *Args) clone() *Args {
	c := *a
	c.Header = maps.Clone(a.Header)
	c.HeaderTemplate = maps.Clone(a.HeaderTemplate)

	return &c
}

// check compiles the templates of a with engine, under the names Run renders them with, without rendering them.
func (a *Args) check(engine vlatte.TemplateEngine) error {
	compile := func(name, text string) error {
		if text == "" {
			return nil
		}

		if _, err := engine.Compile(name, text); err != nil {
			return fmt.Errorf("%w: %w", ErrRenderingTemplate, err)
		}

		return nil
	}

	if err := compile("url_template", a.URLTemplate); err != nil {
		return err
	}

	if err := compile("body_template", a.BodyTemplate); err != nil {
		return err
	}

	for _, k := range slices.Sorted(maps.Keys(a.HeaderTemplate)) {
		if err := compile("header_template "+k, a.HeaderTemplate[k]); err != nil {
			return err
		}
	}

	if a.Multipart != nil {
		if err := a.Multipart.Check(engine); err != nil {
			return fmt.Errorf("webhook: multipart body: %w", err)
		}
	}

	return nil
}

// validate checks what a request needs, and what decoding a map can't check for Args built in Go: one of URL and
// URLTemplate, a URL a request can go to, a valid method, one body at most, a Content-Type only from a multipart body,
// and each header once, as header names ignore case.
func (a *Args) validate() error {
	switch {
	case a.URL == "" && a.URLTemplate == "":
		return errorBadArg(errors.New("one of url and url_template is required"))
	case a.URL != "" && a.URLTemplate != "":
		return errorBadArg(errors.New("url and url_template are both set: keep one of them"))
	case a.URL != "":
		if err := checkURL(a.URL); err != nil {
			return errorBadArg(fmt.Errorf("url: %w", err))
		}
	}

	if a.Method != "" && !mpart.IsToken(a.Method) {
		return errorBadArg(fmt.Errorf("method %q is not an HTTP method", a.Method))
	}

	if countSet(a.Body, a.BodyTemplate, a.BodyFile)+countSet(a.Multipart != nil) > 1 {
		return errorBadArg(errors.New("more than one body is set: keep one of Body, BodyTemplate, BodyFile and Multipart"))
	}

	names := make(map[string]string, len(a.Header)+len(a.HeaderTemplate))
	for _, header := range []map[string]string{a.Header, a.HeaderTemplate} {
		for _, name := range slices.Sorted(maps.Keys(header)) {
			key := textproto.CanonicalMIMEHeaderKey(name)
			if other, ok := names[key]; ok {
				return errorBadArg(fmt.Errorf("headers %s and %s are one header: keep one of them", other, name))
			}

			names[key] = name
		}
	}

	if _, ok := names["Content-Type"]; ok && a.Multipart != nil {
		return errorBadArg(errors.New("a multipart body sets the Content-Type: remove the Content-Type header"))
	}

	return nil
}

// MultipartBody is a multipart body for a webhook, such as one built with mockhttp.MultipartForm.
type MultipartBody interface {
	// Check checks the body and compiles its templates with engine, without writing it.
	Check(engine vlatte.TemplateEngine) error

	// Encode writes the body, with its templates compiled with engine and rendered with data: the data of the request
	// that completed the mock. It returns the body's Content-Type and bytes.
	Encode(engine vlatte.TemplateEngine, data any) (contentType string, body []byte, err error)
}

type argsField = config.Field[Args]

// _argsSchema declares the parameters of a webhook event, the way a mock file's on_complete writes them.
var _argsSchema = config.NewSchema(
	argsField{
		Key: "url", Kind: config.String, Usage: "URL to send the request to.",
		Set: func(a *Args, v config.Value) error { a.URL = v.Str; return nil },
	},
	argsField{
		Key: "url_template", Kind: config.String, Usage: "URL to send the request to, as a template.",
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
		Key: "header_template", Kind: config.Any, Usage: "Headers of the request whose values are templates, by name.",
		Set: func(a *Args, v config.Value) error { return headerOf(v, &a.HeaderTemplate) },
	},
	argsField{
		Key: "body", Kind: config.String, Usage: "Body of the request.",
		Set: func(a *Args, v config.Value) error { a.Body = v.Str; return nil },
	},
	argsField{
		Key: "body_template", Kind: config.String, Usage: "Body of the request, as a template.",
		Set: func(a *Args, v config.Value) error { a.BodyTemplate = v.Str; return nil },
	},
	argsField{
		Key: "body_file", Kind: config.String,
		Usage: "File to read the body of the request from. Relative paths start at the mock file's directory.",
		Set:   func(a *Args, v config.Value) error { a.BodyFile = v.Str; return nil },
	},
	argsField{
		Key: "multipart", Kind: config.Any, Usage: "Multipart body of the request: type, boundary, params and parts.",
		Set: func(a *Args, v config.Value) error { return setMultipart(a, v, false) },
	},
	argsField{
		Key: "multipart_template", Kind: config.Any,
		Usage: "Multipart body whose text is templates: names, filenames, content types, header values, text bodies and body file names.",
		Set:   func(a *Args, v config.Value) error { return setMultipart(a, v, true) },
	},
)

// _exclusive lists the sets of parameters of which only one can be set.
var _exclusive = [][]string{{"url", "url_template"}, {"body", "body_file", "body_template", "multipart", "multipart_template"}}

// argsOf returns the parameters of an event as *Args: a copy of *Args, so a Transform can't change them for later
// events, or a map decoded, whose problems name origin and whose relative files open from dir.
func argsOf(params vlatte.Parameters, origin, dir string) (*Args, error) {
	switch p := params.(type) {
	case nil:
		return nil, errors.New("webhook: arguments must not be nil")
	case *Args:
		if p == nil {
			return nil, errors.New("webhook: arguments must not be nil")
		}

		return p.clone(), nil
	case map[string]any:
		return argsFromMap(p, origin, dir)
	default:
		return nil, errors.New("webhook: arguments must be *Args or map[string]any")
	}
}

// ArgsFromMap decodes the parameters of a webhook event, as a mock file's on_complete writes them:
//   - url, or url_template, a template: a string;
//   - method: a string, GET when empty;
//   - header and header_template, whose values are templates: objects of strings;
//   - body, body_template, a template, or body_file: a string.
//
// As in mock files, each value must have its type, and other keys are errors. A map built in code means what its
// JSON means, so a map[string]string or an int is fine where its JSON is. The error wraps ErrBadArg and lists
// every problem.
func ArgsFromMap(m map[string]any) (*Args, error) {
	return argsFromMap(m, "on_complete", "")
}

// argsFromMap decodes the parameters m, which came from origin, such as "mock users", for problems. Their relative
// files open from dir; "" is the working directory.
func argsFromMap(m map[string]any, origin, dir string) (*Args, error) {
	doc, err := value.ViaJSON(m)
	if err != nil {
		return nil, errorBadArg(err)
	}

	args := &Args{dir: dir}
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

	// Header names ignore case: a name set twice, in either map, is one header with one value.
	names := make(map[string]string)
	for _, headers := range []string{"header", "header_template"} {
		header, _ := params[headers].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(header)) {
			key := "parameters." + headers + "." + name
			canonical := textproto.CanonicalMIMEHeaderKey(name)

			if other, ok := names[canonical]; ok {
				problems = append(problems, config.Problem{
					Code: config.CodeConflict, Origin: origin, Key: key,
					Message: key + " is set with " + other, Hint: "header names ignore case: keep one of them",
				})

				continue
			}

			names[canonical] = key
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

	problems := config.ProblemsOf(v, err)

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
	spec.Dir = a.dir
	a.Multipart = &multipartParams{spec: spec}

	return nil
}

// multipartParams is a multipart body of a webhook's parameters.
type multipartParams struct {
	spec *mpart.Spec
}

// Check implements MultipartBody.
func (m *multipartParams) Check(engine vlatte.TemplateEngine) error {
	_, err := mpart.Compile(m.spec, engine)
	return err
}

// Encode implements MultipartBody.
func (m *multipartParams) Encode(engine vlatte.TemplateEngine, data any) (string, []byte, error) {
	e, err := mpart.Compile(m.spec, engine)
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

// dirOf returns where the relative files of the event's parameters open from, as mockhttp's OnComplete says: the
// directory of the mock file that defines the event, or "" for the working directory.
func dirOf(args vlatte.OnComplete) string {
	if d, ok := args.(interface{ Dir() string }); ok {
		return d.Dir()
	}

	return ""
}

// loggerOf returns the logger the event gives, as mockhttp's OnComplete gives the server's, or one that logs nothing.
func loggerOf(args vlatte.OnComplete) *zerolog.Logger {
	if l, ok := args.(interface{ Logger() *zerolog.Logger }); ok {
		if logger := l.Logger(); logger != nil {
			return logger
		}
	}

	nop := zerolog.Nop()

	return &nop
}

// originOf names where the parameters of an event come from, for problems: the mock that completed.
func originOf(args vlatte.OnComplete) string {
	if mock := args.Mock(); mock != nil {
		return "mock " + mock.ID()
	}

	return "on_complete"
}

// templating returns what the templates of an event compile and render with: the engine of the server and the data
// of the request that completed the mock. Without a Kit, they compile with a vlatte.GoTemplateEngine and no data.
func templating(args vlatte.OnComplete) (vlatte.TemplateEngine, any) {
	kit := args.Kit()
	if kit == nil {
		return _defaultEngine, nil
	}

	engine := kit.TemplateEngine()
	if engine == nil {
		engine = _defaultEngine
	}

	return engine, kit.Data()
}

// checkURL checks that raw is an absolute http or https URL with a host. Its errors show the URL without its
// password, as a *url.Error, which prints the whole URL, wouldn't.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return err
	}

	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s is not an absolute http or https URL", u.Redacted())
	}

	return nil
}

// render compiles text with engine and executes it with data.
func render(engine vlatte.TemplateEngine, name, text string, data any) (string, error) {
	t, err := engine.Compile(name, text)
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
