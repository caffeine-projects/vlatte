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
	"errors"
	"fmt"
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
// The request check is kin-openapi's, which reads each number as a float64 and a const of null as none: a bound or an
// enum value past 2^53 is checked rounded, and const: null takes any value.
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

	unrequireReadOnly(t)

	sp := newSpec(t)
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

// newSpec returns t and its routes: each operation under the path of its first server.
func newSpec(t *openapi3.T) *spec {
	sp := &spec{t: t, byID: make(map[string]*route), byRoute: make(map[string]*route)}

	for path, item := range t.Paths.Map() {
		for method, op := range item.Operations() {
			base := ""
			if op.Servers != nil && len(*op.Servers) > 0 {
				if u, err := url.Parse((*op.Servers)[0].URL); err == nil {
					base = u.Path
				}
			}

			rt := newRoute(t, strings.ToUpper(method), base, path, item, op)
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

	// request is what kin-openapi checks a request against: the operation without its security, which the
	// security check checks.
	request *routers.Route
}

// newRoute returns the route of op, of method, of the path item of path in t, under base, a base path decoded.
func newRoute(t *openapi3.T, method, base, path string, item *openapi3.PathItem, op *openapi3.Operation) *route {
	at := pathtemplate.Join(base, path)
	rt := &route{method: method, path: at, where: pathtemplate.NewRoute(base, path), templated: pathtemplate.Templated(at), security: t.Security}

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
// path, break, as kin-openapi checks them. A body the server didn't keep for its matchers to read again, such as one
// past its parsing limit, isn't checked, nor one in a media type kin-openapi can't read, nor a string against a
// pattern Go can't compile.
func (rt *route) invalid(r *http.Request, params map[string]string) []string {
	req := r.WithContext(r.Context())
	opts := &openapi3filter.Options{
		MultiError:                 true,
		ExcludeReadOnlyValidations: true,
		SkipSettingDefaults:        true,
		AuthenticationFunc:         openapi3filter.NoopAuthenticationFunc,
	}

	switch {
	case r.GetBody != nil:
		body, err := r.GetBody()
		if err != nil {
			opts.ExcludeRequestBody = true
		} else {
			req.Body = body
		}
	case !hasBody(r):
		req.Body = http.NoBody
	default:
		opts.ExcludeRequestBody = true
	}

	err := openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: rt.request, Options: opts,
	})

	return reasons(err)
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

// unrequireReadOnly drops from the required properties of each schema of t, and of the schemas of its allOf, the
// ones it marks readOnly: a client needn't send them. A schema marks readOnly a property that a schema of its allOf,
// or of theirs, gives a schema that is readOnly, or one of whose allOf is: the annotations of an allOf apply to the
// object, whichever branch holds them. kin-openapi's own check, which a 3.1 spec gets only for a schema with a
// reference and no keyword of JSON Schema 2020, misses a property readOnly through its own allOf.
func unrequireReadOnly(t *openapi3.T) {
	var schemas []*openapi3.Schema

	_ = t.WalkSchemas(func(_ string, ref *openapi3.SchemaRef) error {
		if ref.Value != nil {
			schemas = append(schemas, ref.Value)
		}

		return nil
	})

	// The names a schema marks readOnly are marked wherever it applies: it is changed in place. A schema of its allOf
	// may apply elsewhere without them: it is changed in a copy.
	for _, s := range schemas {
		if names := readOnlyNames(s); len(names) > 0 {
			unrequire(s, names, map[*openapi3.Schema]*openapi3.Schema{s: nil})
		}
	}
}

// readOnlyNames returns the names of the properties s marks readOnly.
func readOnlyNames(s *openapi3.Schema) map[string]bool {
	names := make(map[string]bool)

	for _, branch := range allOf(s) {
		for name, p := range branch.Properties {
			if p != nil && p.Value != nil && slices.ContainsFunc(allOf(p.Value), func(x *openapi3.Schema) bool { return x.ReadOnly }) {
				names[name] = true
			}
		}
	}

	return names
}

// allOf returns s and the schemas of its allOf, and of theirs, each once.
func allOf(s *openapi3.Schema) []*openapi3.Schema {
	out := []*openapi3.Schema{s}

	for i := 0; i < len(out); i++ {
		for _, ref := range out[i].AllOf {
			if ref != nil && ref.Value != nil && !slices.Contains(out, ref.Value) {
				out = append(out, ref.Value)
			}
		}
	}

	return out
}

// unrequire drops names from the required properties of s, and replaces each schema of its allOf that requires one
// of them, or whose allOf does, by a copy that doesn't. copies are the copies made, by schema, and nil for each
// schema being changed: one that is reached again, through a cycle, is left as it is.
func unrequire(s *openapi3.Schema, names map[string]bool, copies map[*openapi3.Schema]*openapi3.Schema) {
	s.Required = slices.DeleteFunc(slices.Clone(s.Required), func(name string) bool { return names[name] })

	branches := slices.Clone(s.AllOf)
	for i, ref := range branches {
		if ref == nil || ref.Value == nil || !requiresAny(ref.Value, names) {
			continue
		}

		c, ok := copies[ref.Value]
		if ok && c == nil {
			continue
		}

		if !ok {
			copies[ref.Value] = nil

			branch := *ref.Value
			unrequire(&branch, names, copies)

			c = &branch
			copies[ref.Value] = c
		}

		// Without its reference, kin-openapi checks the copy, not the schema the reference names.
		branches[i] = &openapi3.SchemaRef{Value: c}
	}

	s.AllOf = branches
}

// requiresAny reports whether s, or a schema of its allOf, requires one of names.
func requiresAny(s *openapi3.Schema, names map[string]bool) bool {
	return slices.ContainsFunc(allOf(s), func(x *openapi3.Schema) bool {
		return slices.ContainsFunc(x.Required, func(name string) bool { return names[name] })
	})
}
