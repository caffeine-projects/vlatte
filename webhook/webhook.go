package webhook

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/bodyfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.EventListener[vlatte.OnComplete] = (*WebHook)(nil)
	_ vlatte.EventPreparer                    = (*WebHook)(nil)
	_ descriptor.SelfDescriptive              = (*Args)(nil)
	_ descriptor.SelfDescriptive              = (*multipartParams)(nil)
	_ vlatte.Kit                              = noKit{}
)

var (
	ErrBadArg            = errors.New("webhook: bad argument")
	ErrRenderingTemplate = errors.New("webhook: rendering error")
)

// _defaultEngine compiles the templates of a webhook run without a Kit, such as one called directly, or with a Kit
// without an engine.
var _defaultEngine = vlatte.MustNewGoTemplateEngine()

// _nopLogger is the logger of a webhook run without a Kit.
var _nopLogger = zerolog.Nop()

// _maxAnswer is how much of its target's answer a send reads before it ends, as net/http drains of a body closed
// unread: past it, the connection closes.
const _maxAnswer = 256 << 10

type Option func(*WebHook) error

// Transform lets users transform and customize the [WebHook] event args and the [Args]
// before they are used. It runs every time [WebHook.Run] is called.
type Transform func(ctx context.Context, args vlatte.OnComplete, webhookArgs *Args) error

// WebHook is an automated HTTP request sent when an on-complete event is fired.
// Register it with [vlatte.Listener.OnComplete] (e.g. via mockhttp.Setup().OnCompleteListener(name, wh)).
// mockhttp sends it once the mock replied, and the response waits for it: a test checks the target right after.
//
// It logs each send and its answer at info level, with WithLogger's logger, or else with the server's, which the
// event's [vlatte.Kit] gives. It sends with WithTransport's transport, or else with the server's, from the Kit too:
// mockhttp gives its Config.Transport, or else one it closes with the server, as mockgrpc does. Relative files, such
// as a body_file, open from the Kit's Dir: the directory of the mock file. An event without a Kit logs nothing,
// sends with http.DefaultTransport, and opens files from the working directory. A send fails when the target can't be
// reached in time, or answers with a status outside 2xx; a redirect isn't followed. A send reads up to 256 KiB of the
// answer, within its time, so its connection carries the next one. The time is WithTimeout's, or else the event's:
// mockhttp gives the server's Config.WebhookTimeout. Without either, it is 10s. A zero WebHook is ready to use.
type WebHook struct {
	transport http.RoundTripper
	transform Transform
	logger    *zerolog.Logger
	timeout   time.Duration
}

// New creates a new WebHook instance.
func New(options ...Option) (*WebHook, error) {
	w := &WebHook{}

	for i, opt := range options {
		if err := opt(w); err != nil {
			return nil, fmt.Errorf("webhook: option at index %d: %w", i, err)
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
	kit := args.Kit()
	if kit == nil {
		kit = noKit{}
	}

	// Each run has its own copy of the parameters, so a Transform changes only this request.
	webhookArgs, err := argsOf(args.Parameters(), originOf(args), kit.Dir())
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

	if w.transform != nil {
		if err := w.transform(ctx, args, webhookArgs); err != nil {
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

	engine, data := kit.TemplateEngine(), kit.Data()
	if engine == nil {
		engine = _defaultEngine
	}

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
		b, err := os.ReadFile(bodyfile.Path(webhookArgs.dir, webhookArgs.BodyFile))
		if err != nil {
			return fmt.Errorf("webhook: error reading body_file %s: %w", webhookArgs.BodyFile, err)
		}

		body = bytes.NewReader(b)
	case len(webhookArgs.Body) > 0:
		body = strings.NewReader(webhookArgs.Body)
	}

	logger := w.logger
	if logger == nil {
		logger = kit.Logger()
	}

	// With a context, only a bad method or URL fails it, and validate and checkURL checked both.
	req, _ := http.NewRequestWithContext(ctx, method, u.String(), body)

	for k, v := range webhookArgs.Header {
		req.Header.Set(k, v)
	}

	for k, v := range webhookArgs.HeaderTemplate {
		rendered, err := render(engine, "header_template "+k, v, data)
		if err != nil {
			return err
		}

		// As in replies: the value goes without the spaces and line breaks around it, and a line break inside, which
		// would end the header there, fails here, whatever the Transport would do with it.
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

	// The URL may hold a password: logs and errors show it redacted.
	redacted := u.Redacted()

	// Everything renders before the delay, so a mistake shows without waiting for it.
	if webhookArgs.Delay > 0 {
		if err := httpx.Wait(ctx, webhookArgs.Delay); err != nil {
			return fmt.Errorf("webhook: %s %s: waiting for the delay of %s: %w", method, redacted, webhookArgs.Delay, err)
		}
	}

	logger.Info().
		Str("url", redacted).
		Str("kind", args.Kind().String()).
		Msgf("---> WEBHOOK REQUEST %s %s", method, u.Path)

	// The timeout counts from the send, after the delay.
	timeout := cmp.Or(w.timeout, timeoutOf(args), httpx.DefaultWebhookTimeout)

	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	roundTripper := w.transport
	if roundTripper == nil {
		roundTripper = kit.Transport()
	}

	// The caller logs the errors: logging them here too would log each twice.
	res, err := roundTripper.RoundTrip(req.WithContext(sendCtx))
	if err != nil {
		return fmt.Errorf("webhook: %s %s: request failed: %w", method, redacted, err)
	}

	// The answer is read, up to a limit, while the send's timeout holds, so its connection carries the next send:
	// closed unread, net/http drains it after the send for at most 50ms, and loses the connection when that's too short.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, _maxAnswer))
		res.Body.Close()
	}()

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

	// Delay is how long the webhook waits, once the mock replied, before it sends the request. The response waits for
	// it too. The WebHook's timeout counts from the send.
	Delay time.Duration

	dir string // where relative files open from: the mock file's directory; "" is the working directory
}

// Describe implements descriptor.SelfDescriptive: the parameters as a mock file writes them, the ones set only, so the
// description of a mock, such as the admin API gives, loads back with the same webhook. A relative body file is joined
// to the mock file's directory, which a description loaded back doesn't have. A multipart body is described when it
// describes itself, as mockhttp's do.
func (a *Args) Describe() descriptor.Descriptor {
	params := make(map[string]any)

	for key, value := range map[string]string{
		"url": a.URL, "url_template": a.URLTemplate, "method": a.Method,
		"body": a.Body, "body_template": a.BodyTemplate, "body_file": bodyfile.Path(a.dir, a.BodyFile),
	} {
		if value != "" {
			params[key] = value
		}
	}

	if len(a.Header) > 0 {
		params["header"] = maps.Clone(a.Header)
	}

	if len(a.HeaderTemplate) > 0 {
		params["header_template"] = maps.Clone(a.HeaderTemplate)
	}

	if body, ok := a.Multipart.(descriptor.SelfDescriptive); ok {
		d := body.Describe()
		params[d.Key] = d.Props
	}

	if a.Delay > 0 {
		params["delay"] = a.Delay.String()
	}

	return descriptor.New("parameters", params)
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
// URLTemplate, a URL a request can go to, a valid method, one body at most, a delay of 0 or more, a Content-Type only
// from a multipart body, and each header once, as header names ignore case.
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

	if a.Method != "" && !httpx.IsToken(a.Method) {
		return errorBadArg(fmt.Errorf("method %q is not an HTTP method", a.Method))
	}

	if countSet(a.Body, a.BodyTemplate, a.BodyFile)+countSet(a.Multipart != nil) > 1 {
		return errorBadArg(errors.New("more than one body is set: keep one of Body, BodyTemplate, BodyFile and Multipart"))
	}

	if a.Delay < 0 {
		return errorBadArg(fmt.Errorf("delay %s is negative", a.Delay))
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
	argsField{
		Key: "delay", Kind: config.Duration, Usage: "How long to wait before sending, once the mock replied; the response waits for it too: milliseconds or a Go duration (1.5s).",
		Set: func(a *Args, v config.Value) error { a.Delay = v.Dur; return nil },
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
//   - one body: body, body_template, a template, or body_file, a string; or multipart, or multipart_template, whose
//     text is templates: an object with the type, boundary, params and parts of the body;
//   - delay: milliseconds, or a Go duration such as 1.5s.
//
// As in mock files, each value must have its type, and other keys are errors. The webhook's checks of what a request
// needs, such as one of url and url_template, come when the mock is built. A map built in code means what its
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

// Describe implements descriptor.SelfDescriptive: the body as the parameters of a mock file write it.
func (m *multipartParams) Describe() descriptor.Descriptor {
	return describeMultipart(m.spec)
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

// describeMultipart describes a multipart body as the parameters of a mock file write it: under multipart_template
// when its text is templates, else under multipart.
func describeMultipart(spec *mpart.Spec) descriptor.Descriptor {
	if spec.Templated {
		return descriptor.New("multipart_template", spec.Doc())
	}

	return descriptor.New("multipart", spec.Doc())
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

// timeoutOf returns how long a send waits for its answer as the event says, as mockhttp's OnComplete gives the server's
// Config.WebhookTimeout, or 0 when it doesn't say.
func timeoutOf(args vlatte.OnComplete) time.Duration {
	if t, ok := args.(interface{ WebhookTimeout() time.Duration }); ok {
		return max(t.WebhookTimeout(), 0)
	}

	return 0
}

// originOf names where the parameters of an event come from, for problems: the mock that completed.
func originOf(args vlatte.OnComplete) string {
	if mock := args.Mock(); mock != nil {
		return "mock " + mock.ID()
	}

	return "on_complete"
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

// noKit is the Kit of a webhook run without one, such as one called directly: its templates compile with a
// vlatte.GoTemplateEngine and render with no data, it logs nothing, sends with http.DefaultTransport, and opens
// relative files from the working directory.
type noKit struct{}

func (noKit) TemplateEngine() vlatte.TemplateEngine { return _defaultEngine }
func (noKit) Data() any                             { return nil }
func (noKit) Logger() *zerolog.Logger               { return &_nopLogger }
func (noKit) Transport() http.RoundTripper          { return http.DefaultTransport }
func (noKit) Dir() string                           { return "" }
