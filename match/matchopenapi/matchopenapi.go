// Package matchopenapi holds the openapi matcher of mock files, [openapi, {spec, operation, check}], which matches a
// request that keeps the contract an OpenAPI 3 spec gives one of its operations. Under not, it matches a request that
// breaks it: the mocks vlatte openapi writes refuse such a request with the error the spec's server would answer.
//
// The vlatte command knows the matcher. A server of this module loads the mocks that use it once its registry has it,
// which links kin-openapi, an OpenAPI library, only into the programs that ask for it:
//
//	m := mockhttp.MustNew(mockhttp.Setup().Matchers(matchbuild.MustNew(matchopenapi.WithMatcher())))
package matchopenapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/openapi/pathtemplate"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

var (
	_ vlatte.Matcher         = (*matcher)(nil)
	_ vlatte.SelfDescriptive = (*matcher)(nil)
)

// Name is the name of the matcher in mock files.
const Name = "openapi"

// The parts of the contract the matcher checks, as its check argument names them.
const (
	_checkSecurity    = "security"
	_checkContentType = "content_type"
	_checkRequest     = "request"
)

// WithMatcher returns the option that adds the openapi matcher to a [matchbuild.Registry]. Its arguments are an
// object: spec, the file of an OpenAPI 3 spec, relative to the mock file, in which every reference is local;
// operation, the operationId of one of its operations, or its method and path, such as GET /pets/{id}, the path under
// the path of the operation's first server; and check, what the request must keep: security, a credential of one of
// the operation's security requirements; content_type, a body in a media type the operation takes; or request, its
// parameters and body as the spec describes them, but for the readOnly properties it requires, which a client needn't
// send. A request of another operation keeps the contract, such as one of /pets/mine, of any method, for the
// operations of /pets/{id}: OpenAPI matches the path before the method, and a path of fewer templated segments first.
// The matcher compares a request's path as the mocks vlatte openapi writes of the operation do, under the path of
// its first server: GET /foo%2Fbar is a request of GET /foo/bar.
//
// The request check reads and checks parameters as kin-openapi does, each number as a float64. It checks a body
// against the schema of its media type as the spec writes it, an integer past 2^53 and a const of null included, with
// JSON Schema 2020-12, which an OpenAPI 3.0 schema is translated to, keeping what kin-openapi checks besides: a oneOf
// or an anyOf with a discriminator takes an object by the branch the discriminator names, a nullable schema of
// OpenAPI 3.0 takes null, and only the formats kin-openapi checks are, byte, date, date-time, int32 and int64. A body
// other than JSON is read as kin-openapi reads it, a number of a form as a float64. A body whose schema doesn't
// compile, such as one that refers into the paths of the spec, is checked as kin-openapi checks it.
//
// The matcher records what a request breaks in its explanation, and never fails: a spec that doesn't load fails the
// mock file. The matchers of a registry load each spec once, and again when its file changes.
func WithMatcher() matchbuild.Option {
	s := &specs{loaded: make(map[string]*spec)}
	return matchbuild.WithMatcher(Name, s.build)
}

// specs are the specs the matchers of a registry loaded, by the absolute path of their file.
type specs struct {
	mu     sync.Mutex
	loaded map[string]*spec
}

// build builds the matcher of args, as a [matchbuild.BuildFunc].
func (s *specs) build(r *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
	var obj map[string]any
	if len(args) == 1 {
		obj, _ = args[0].(map[string]any)
	}

	if obj == nil {
		return nil, fmt.Errorf("expected one object of spec, operation and check, not %v", args)
	}

	for key := range obj {
		if !slices.Contains([]string{"spec", "operation", "check"}, key) {
			return nil, fmt.Errorf("unknown key %q: expected spec, operation and check", key)
		}
	}

	file, _ := obj["spec"].(string)
	operation, _ := obj["operation"].(string)
	check, _ := obj["check"].(string)

	switch {
	case file == "":
		return nil, fmt.Errorf("spec: expected the file of a spec, not %v", obj["spec"])
	case operation == "":
		return nil, fmt.Errorf("operation: expected an operationId, or a method and a path, not %v", obj["operation"])
	case !slices.Contains([]string{_checkSecurity, _checkContentType, _checkRequest}, check):
		return nil, fmt.Errorf("check: expected security, content_type or request, not %v", obj["check"])
	}

	path, err := filepath.Abs(r.Path(file))
	if err != nil {
		return nil, fmt.Errorf("spec: resolving %s: %w", file, err)
	}

	sp, err := s.load(path)
	if err != nil {
		return nil, err
	}

	rt := sp.byID[operation]
	if method, p, ok := strings.Cut(operation, " "); ok && rt == nil {
		rt = sp.byRoute[strings.ToUpper(method)+" "+p]
	}

	if rt == nil {
		return nil, fmt.Errorf("operation: no operation %q in %s: expected an operationId, or a method and a path, such as GET /pets", operation, path)
	}

	return &matcher{spec: sp, route: rt, check: check, desc: map[string]any{"spec": path, "operation": operation, "check": check}}, nil
}

// load returns the spec of the file at path: the one loaded before, unless the file changed since.
func (s *specs) load(path string) (*spec, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("spec: reading %s: %w", path, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if sp := s.loaded[path]; sp != nil && sp.modTime.Equal(info.ModTime()) && sp.size == info.Size() {
		return sp, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spec: reading %s: %w", path, err)
	}

	t, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("spec: loading %s: %w", path, err)
	}

	sp := newSpec(t, openapiv3.RequestBodies(t, data, &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}))
	sp.modTime, sp.size = info.ModTime(), info.Size()
	s.loaded[path] = sp

	return sp, nil
}

// spec is a loaded spec and its routes.
type spec struct {
	t       *openapi3.T
	byID    map[string]*route // by operationId
	byRoute map[string]*route // by method and route, such as GET /v1/pets/{id}
	routes  []*route          // all of them, by number of templated segments
	modTime time.Time
	size    int64
}

// newSpec returns t and its routes: each operation under the path of its first server, its bodies checked against
// roots, the schema of each media type, as openapiv3.RequestBodies gives them.
func newSpec(t *openapi3.T, roots map[*openapi3.MediaType]map[string]any) *spec {
	sp := &spec{t: t, byID: make(map[string]*route), byRoute: make(map[string]*route)}

	bodies := make(map[*openapi3.MediaType]*body, len(roots))
	for mt, root := range roots {
		bodies[mt] = newBody(mt, root, t.IsOpenAPI30())
	}

	for path, item := range t.Paths.Map() {
		for method, op := range item.Operations() {
			base := ""
			if op.Servers != nil && len(*op.Servers) > 0 {
				if u, err := url.Parse((*op.Servers)[0].URL); err == nil {
					base = u.Path
				}
			}

			rt := newRoute(t, strings.ToUpper(method), base, path, item, op, bodies)
			sp.byRoute[rt.method+" "+rt.path] = rt
			sp.routes = append(sp.routes, rt)

			if op.OperationID != "" {
				sp.byID[op.OperationID] = rt
			}
		}
	}

	slices.SortStableFunc(sp.routes, func(a, b *route) int { return a.templated - b.templated })

	return sp
}

// routedElsewhere reports whether r, a request to a path rt matches, goes to another path: one that matches it with
// fewer templated segments, of any method, as OpenAPI matches /pets/mine before /pets/{id}, and the path before the
// method.
func (sp *spec) routedElsewhere(r *http.Request, rt *route) bool {
	for _, other := range sp.routes {
		if other.templated >= rt.templated {
			return false
		}

		if _, ok := other.where.Match(r.URL); ok {
			return true
		}
	}

	return false
}

// route is an operation of a spec, where it answers.
type route struct {
	method    string
	path      string              // the path of the spec under the path of its first server, such as /v1/pets/{id}
	where     *pathtemplate.Route // how its mocks compare a request's path
	templated int                 // the number of segments of path that hold a parameter

	// security are the requirements of the operation, else of the spec, of which a request keeps one.
	security openapi3.SecurityRequirements
	schemes  openapi3.SecuritySchemes // the security schemes of the spec, by name
	content  openapi3.Content         // the media types the request body takes, or nil for none
	consumes []string                 // the keys of content, sorted

	// bodies are how the matcher checks a body, by media type, of the spec's operations.
	bodies map[*openapi3.MediaType]*body

	// request is what kin-openapi checks a request against: the operation without its security, which the
	// security check checks, and without the schemas of its body, which bodies check.
	request *routers.Route
}

// newRoute returns the route of op, of method, of the path item of path in t, under base, a base path decoded, which
// checks a body of a media type bodies has as it does, and any other as kin-openapi does.
func newRoute(t *openapi3.T, method, base, path string, item *openapi3.PathItem, op *openapi3.Operation, bodies map[*openapi3.MediaType]*body) *route {
	at := pathtemplate.Join(base, path)
	rt := &route{
		method: method, path: at, where: pathtemplate.NewRoute(base, path), templated: pathtemplate.Templated(at), security: t.Security,
		bodies: bodies,
	}

	if op.Security != nil {
		rt.security = *op.Security
	}

	if t.Components != nil {
		rt.schemes = t.Components.SecuritySchemes
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil && len(op.RequestBody.Value.Content) > 0 {
		rt.content = op.RequestBody.Value.Content
		rt.consumes = make([]string, 0, len(rt.content))

		for mt := range rt.content {
			rt.consumes = append(rt.consumes, mt)
		}

		slices.Sort(rt.consumes)
	}

	unsecured := *op
	unsecured.Security = &openapi3.SecurityRequirements{}

	// kin-openapi still checks that a body the operation requires is there, in a media type it takes.
	if rt.content != nil {
		rb := *op.RequestBody.Value
		rb.Content = make(openapi3.Content, len(rt.content))

		for name, mt := range rt.content {
			rb.Content[name] = mt
			if bodies[mt] != nil {
				unchecked := *mt
				unchecked.Schema = nil
				rb.Content[name] = &unchecked
			}
		}

		unsecured.RequestBody = &openapi3.RequestBodyRef{Value: &rb}
	}

	rt.request = &routers.Route{Spec: t, Path: path, PathItem: item, Method: method, Operation: &unsecured}

	return rt
}

// matcher matches a request that keeps the contract of the operation of route, for its check.
type matcher struct {
	spec  *spec
	route *route
	check string
	desc  map[string]any // its arguments, the spec's file absolute
}

// Name implements vlatte.Matcher.
func (m *matcher) Name() string { return "OpenAPI" }

// Describe implements vlatte.SelfDescriptive.
func (m *matcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg(Name, m.desc)
}

// Match implements vlatte.Matcher. It matches a request of another operation, and records what a request of its
// own breaks.
func (m *matcher) Match(arg vlatte.MatcherArg) (bool, error) {
	r, ok := arg.Value().(*http.Request)
	if !ok {
		arg.Explain().AddMessage(fmt.Sprintf("openapi: expected a request, under request.custom, not %T", arg.Value()))
		return false, nil
	}

	params, own := m.route.where.Match(r.URL)
	if !own || !strings.EqualFold(r.Method, m.route.method) || m.spec.routedElsewhere(r, m.route) {
		return true, nil
	}

	var broken []string

	switch m.check {
	case _checkSecurity:
		broken = m.route.missingCredentials(r)
	case _checkContentType:
		broken = m.route.unsupportedMediaType(r)
	default:
		broken = m.route.invalid(r, params)
	}

	for _, message := range broken {
		arg.Explain().AddMessage(message)
	}

	return len(broken) == 0, nil
}

// missingCredentials returns what r lacks to keep one of the security requirements of rt, or nil when it keeps one.
func (rt *route) missingCredentials(r *http.Request) []string {
	if len(rt.security) == 0 {
		return nil
	}

	expected := make([]string, 0, len(rt.security))

	for _, requirement := range rt.security {
		var missing []string

		for _, name := range slices.Sorted(maps.Keys(requirement)) {
			if s := rt.schemes[name]; s != nil && s.Value != nil {
				if what, ok := credential(r, s.Value); !ok {
					missing = append(missing, what)
				}
			}
		}

		if len(missing) == 0 {
			return nil
		}

		expected = append(expected, strings.Join(missing, " and "))
	}

	return []string{"security: expected " + strings.Join(expected, ", or ")}
}

// credential returns what r must hold for scheme s, and whether it does. A scheme of a type it doesn't know is kept.
func credential(r *http.Request, s *openapi3.SecurityScheme) (string, bool) {
	scheme, _, _ := strings.Cut(r.Header.Get("Authorization"), " ")

	switch s.Type {
	case "apiKey":
		switch s.In {
		case "query":
			return "the query parameter " + s.Name, r.URL.Query().Has(s.Name)
		case "cookie":
			_, err := r.Cookie(s.Name)
			return "the cookie " + s.Name, err == nil
		default:
			return "the header " + s.Name, len(r.Header.Values(s.Name)) > 0
		}
	case "http":
		return "an Authorization header of scheme " + s.Scheme, scheme != "" && strings.EqualFold(scheme, s.Scheme)
	case "oauth2", "openIdConnect":
		return "an Authorization header", scheme != ""
	case "mutualTLS":
		return "a client certificate", r.TLS != nil && len(r.TLS.PeerCertificates) > 0
	default:
		return "", true
	}
}

// unsupportedMediaType returns why the body of r is in a media type rt doesn't take, or nil when it takes it or r
// has none.
func (rt *route) unsupportedMediaType(r *http.Request) []string {
	mediaType := r.Header.Get("Content-Type")
	if rt.content == nil || !hasBody(r) || rt.content.Get(mediaType) != nil {
		return nil
	}

	return []string{fmt.Sprintf("content type %q: expected one of: %s", mediaType, strings.Join(rt.consumes, ", "))}
}

// hasBody reports whether r has a body: one of a length, or of none that the server didn't read empty.
func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody
}

// invalid returns what the parameters and body of r, whose path holds the values params of the parameters of rt's
// path, break: its parameters as kin-openapi checks them, and its body against the schema of its media type, as the
// spec writes it, or as kin-openapi checks it when that schema doesn't compile. A body the server didn't keep for its
// matchers to read again, such as one past its parsing limit, isn't checked, nor one in a media type kin-openapi
// can't read, nor a string against a pattern Go can't compile.
func (rt *route) invalid(r *http.Request, params map[string]string) []string {
	req := r.WithContext(r.Context())
	opts := &openapi3filter.Options{
		MultiError:                 true,
		ExcludeReadOnlyValidations: true,
		SkipSettingDefaults:        true,
		AuthenticationFunc:         openapi3filter.NoopAuthenticationFunc,
	}

	var data []byte

	switch {
	case r.GetBody != nil:
		var err error
		if data, err = readAgain(r); err != nil {
			opts.ExcludeRequestBody = true
		} else {
			req.Body = io.NopCloser(bytes.NewReader(data))
		}
	case !hasBody(r):
		req.Body = http.NoBody
	default:
		opts.ExcludeRequestBody = true
	}

	err := openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: rt.request, Options: opts,
	})

	broken := reasons(err)

	mt := rt.content.Get(r.Header.Get("Content-Type"))
	if b := rt.bodies[mt]; b != nil && len(data) > 0 {
		messages, checked := b.check(data, r.Header)
		if !checked {
			messages = rt.kinBody(r, data, mt, opts)
		}

		broken = append(broken, messages...)
	}

	return broken
}

// kinBody returns what data, the body of r, of the media type mt, breaks, as kin-openapi checks it with opts.
func (rt *route) kinBody(r *http.Request, data []byte, mt *openapi3.MediaType, opts *openapi3filter.Options) []string {
	req := r.WithContext(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(data))

	content := make(openapi3.Content, 1)
	for name, other := range rt.content {
		if other == mt {
			content[name] = mt
		}
	}

	err := openapi3filter.ValidateRequestBody(r.Context(), &openapi3filter.RequestValidationInput{Request: req, Options: opts}, &openapi3.RequestBody{Content: content})
	if err == nil {
		return nil
	}

	return reasons(openapi3.MultiError{err})
}

// readAgain returns the body of r, which GetBody gives again.
func readAgain(r *http.Request) ([]byte, error) {
	body, err := r.GetBody()
	if err != nil {
		return nil, err
	}

	defer body.Close()

	return io.ReadAll(body)
}

// reasons returns what err, an error of openapi3filter.ValidateRequest with MultiError, says a request breaks, one
// message for each thing.
func reasons(err error) []string {
	var out []string

	all, _ := errors.AsType[openapi3.MultiError](err)
	for _, e := range all {
		re, ok := errors.AsType[*openapi3filter.RequestError](e)
		if !ok {
			out = append(out, e.Error())
			continue
		}

		where := "body"
		if p := re.Parameter; p != nil {
			where = fmt.Sprintf("%s parameter %q", p.In, p.Name)
		}

		if re.Err == nil {
			out = append(out, where+": "+re.Reason)
			continue
		}

		if pe, ok := errors.AsType[*openapi3filter.ParseError](re.Err); ok && pe.Kind == openapi3filter.KindUnsupportedFormat {
			continue
		}

		causes := []error{re.Err}
		if me, ok := errors.AsType[openapi3.MultiError](re.Err); ok {
			causes = me
		}

		// The JSON Schema 2020-12 validator finds what a value breaks in no fixed order: a reply lists it sorted.
		var messages []string

		for _, cause := range causes {
			if _, ok := errors.AsType[*openapi3.SchemaPatternRegexError](cause); ok {
				continue
			}

			se, ok := errors.AsType[*openapi3.SchemaError](cause)
			if !ok {
				message := cause.Error()
				if re.Reason != "" && re.Reason != message {
					message = re.Reason + ": " + message
				}

				messages = append(messages, where+": "+message)

				continue
			}

			at := ""
			if pointer := se.JSONPointer(); len(pointer) > 0 {
				at = "/" + strings.Join(pointer, "/")
			}

			subject := where
			if at != "" {
				subject += " at " + at
			}

			messages = append(messages, subject+": "+reason(se, at))
		}

		slices.Sort(messages)
		out = append(out, messages...)
	}

	return out
}

// reason returns why se, about the value at, fails, in one line: the first of its reason, which lists below it why
// each schema of a oneOf fails, say, without the location kin-openapi's JSON Schema 2020-12 validator, which checks a
// 3.1 spec, starts it with, such as error at "/age": at '/age': .
func reason(se *openapi3.SchemaError, at string) string {
	r, _, _ := strings.Cut(se.Reason, "\n")
	r = strings.TrimPrefix(r, `error at "`+at+`": `)

	return strings.TrimPrefix(r, "at '"+at+"': ")
}
