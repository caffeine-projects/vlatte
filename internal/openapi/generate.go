package openapi

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"fmt"
	"hash/fnv"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/fakefy"
	"github.com/caffeine-projects/vlatte/internal/openapi/pathtemplate"
)

// The priority bands of generated mocks, which order the mocks of a route. [priority] puts them after the bands of
// the routes with fewer templated segments.
const (
	_bandDisabled         = 100
	_bandUnauthorized     = 200
	_bandUnsupportedMedia = 300
	_bandBadRequest       = 400
	_bandExample          = 500
	_bandSuccess          = 600
	_bandNotAcceptable    = 700
	_bandNotFound         = 800
	_bandNotAllowed       = 900
)

// _problemJSON is the media type of the bodies the generator writes for a request it refuses: RFC 9457's.
const _problemJSON = "application/problem+json"

// _problemTemplate is the body of a 400 or 422 the spec gives none for: it lists what the request breaks, as the
// openapi matcher records it.
const _problemTemplate = `{"title":"%s","status":%d,"errors":{{ toJSON .Explanation.Messages }}}`

// _routeStep is how far apart [priority] puts the bands of routes whose number of templated segments differs by one:
// past every band.
const _routeStep = 1000

// ValueKeywords are the keywords of a schema that hold values, not schemas: what they hold is data, whatever its keys.
var ValueKeywords = []string{"const", "default", "enum", "example", "examples"}

// SchemaMapKeywords are the keywords of a schema that hold schemas by name: their keys are names, not keywords.
var SchemaMapKeywords = []string{"properties", "patternProperties", "$defs", "dependentSchemas"}

// _contractStatuses are the statuses a request's contract decides: only the mocks that refuse a request that breaks
// it answer them.
var _contractStatuses = []string{"400", "401", "405", "415", "422"}

// Mock is a generated mock: the route and response it answers, and the request it matches.
type Mock struct {
	// ID is the mock's ID, which its file is named after. [Generate] sets it with [RouteFirst].
	ID string

	// Route is the path the mock answers, under the document's base path, as the spec writes it.
	Route string

	// Method is the HTTP method, in upper case, or "" for a 405, which answers the methods its route lacks.
	Method string

	// Status is the key of the response the mock answers with, such as 200, 4XX or default.
	Status string

	// MediaType is the media type of the response body, or "" for none.
	MediaType string

	// Variant tells mocks of one response apart: the name of an example, or "example" for an unnamed one. It is ""
	// for the response's main mock.
	Variant string

	// Name describes the mock.
	Name string

	// Priority orders the mock among the others: lower first.
	Priority int

	// Enabled is false for a mock that nothing in a request predicts, which the admin API can enable.
	Enabled bool

	// Request is what the mock matches.
	Request Request

	// Reply is what the mock answers.
	Reply Reply
}

// Request is what a generated mock matches.
type Request struct {
	// Method is what the method matches, as a mock file's method key takes it: a method, or a matcher list.
	Method any

	// Path is what the path matches, as a mock file's path key takes it: an equal matcher list, for a route without
	// parameters or an example of one; or a regex matcher list, for a route with a parameter inside a segment. It is
	// nil for a PathTemplate.
	Path any

	// PathTemplate is the path template to match, for a route whose parameters are whole segments.
	PathTemplate string

	// Accept is the matcher spec of the Accept header, or nil for none.
	Accept []any

	// Custom are the matcher specs of the whole request, as a mock file's custom key takes them, or nil for none.
	Custom []any
}

// Reply is what a generated mock answers.
type Reply struct {
	// Status is the status code.
	Status int

	// Header holds the headers, by name.
	Header map[string]string

	// Body is the body, as a mock file's body key takes it: text as is, anything else as JSON. nil is no body.
	Body any

	// BodyBase64 is the body as bytes, in standard base64, in place of Body. "" is none.
	BodyBase64 string

	// Template tells whether Body is a template, which the server renders for each request.
	Template bool
}

// Generate makes the mock files of doc: mocks named by [RouteFirst], each in a file of its own, as [PerMock] lays
// them out. It logs on log, at warn level, each thing it could not honor, naming its mock, and [zerolog.Nop] silences
// it: a body no value fits, by its examples or its schema, leaves its mock without one and disabled. It fails only when
// ctx is done.
func Generate(ctx context.Context, doc *Document, log *zerolog.Logger) ([]File, error) {
	g := generator{doc: doc, responses: newView(doc.Defs, "writeOnly", false), spec: specName(doc), routes: make(map[string]*routeMethods)}

	ops := slices.Clone(doc.Operations)
	slices.SortFunc(ops, func(a, b Operation) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Method, b.Method))
	})

	for _, op := range ops {
		if err := stopped(ctx); err != nil {
			return nil, err
		}

		g.operation(op)
	}

	// The last operation's values may take long too.
	if err := stopped(ctx); err != nil {
		return nil, err
	}

	for _, r := range slices.Sorted(maps.Keys(g.routes)) {
		g.notAllowed(r, g.routes[r])
	}

	name(g.mocks, RouteFirst)

	for _, w := range g.warnings {
		m := g.mocks[w.mock]
		e := log.Warn().Str("mock", m.ID).Str("method", m.Method).Str("route", m.Route).Str("status", m.Status)

		if m.MediaType != "" {
			e = e.Str("media_type", m.MediaType)
		}

		if m.Variant != "" {
			e = e.Str("variant", m.Variant)
		}

		e.Msg(w.message)
	}

	return append(PerMock(g.mocks), File{Name: g.spec, Data: doc.Spec}), nil
}

// specName returns the name of the file of the copy of doc's spec: its title, then a hash of the copy. A rerun on a
// changed spec writes a copy of another name, and leaves the one the mocks it no longer writes check requests against.
func specName(doc *Document) string {
	data, _ := json.Marshal(doc.Spec, json.Deterministic(true))

	h := fnv.New32a()
	h.Write(data)

	return fmt.Sprintf("%s.%08x.openapi.yaml", cmp.Or(part(doc.Title), "spec"), h.Sum32())
}

// stopped returns the error of generating the mocks for ctx, done, or nil.
func stopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("openapi: generating the mocks: %w", err)
	}

	return nil
}

// generator holds the mocks and warnings of a document as they are made.
type generator struct {
	doc       *Document
	responses *view                    // how a response sees the document's schemas
	spec      string                   // the name of the file of the copy of the spec, beside the mocks
	routes    map[string]*routeMethods // the methods of each route, for its 405
	mocks     []Mock
	pending   []string
	warnings  []warning
}

// routeMethods are the methods of a route, a path of the spec under its base path, and what its mocks match.
type routeMethods struct {
	request Request  // what the mocks of the route match, but for their method
	methods []string // the methods of its operations
}

// warning is something the generator could not honor in a mock.
type warning struct {
	mock    int // the index of the mock in generator.mocks
	message string
}

// add adds m, with the warnings recorded since the mock before it.
func (g *generator) add(m Mock) {
	for _, message := range g.pending {
		g.warnings = append(g.warnings, warning{mock: len(g.mocks), message: message})
	}

	g.pending = g.pending[:0]
	g.mocks = append(g.mocks, m)
}

// warnf records a warning about the next mock [generator.add] adds: a mock being made, or the first of a response's.
func (g *generator) warnf(format string, args ...any) {
	g.pending = append(g.pending, fmt.Sprintf(format, args...))
}

// operation makes the mocks of op.
func (g *generator) operation(op Operation) {
	route := pathtemplate.Join(op.BasePath, op.Path)
	request := routeRequest(op.Method, pathtemplate.NewRoute(op.BasePath, op.Path))

	primary := primaryResponse(op.Responses)
	notFound := findResponse(op.Responses, "404")

	// Only the success answers the examples: without one, they make no mock to warn on.
	var examples []pathExample
	if primary != nil {
		examples = g.pathExamples(op)
	}
	base := Mock{Route: route, Method: op.Method, Name: cmp.Or(op.Summary, op.Method+" "+route), Request: request}

	if g.routes[route] == nil {
		g.routes[route] = &routeMethods{request: request}
	}

	g.routes[route].methods = append(g.routes[route].methods, op.Method)
	g.contract(op, base)

	for _, r := range op.Responses {
		if slices.Contains(_contractStatuses, r.Status) || r.Status == "406" || r.Status == "404" && primary != nil {
			continue
		}

		m := base
		m.Status = r.Status
		r = g.withoutRanges(m, r)

		if primary == nil || r.Status != primary.Status {
			m.Priority = priority(_bandDisabled, route)
			g.contents(m, r, statusCode(r.Status, http.StatusInternalServerError), true)

			continue
		}

		code := statusCode(r.Status, http.StatusOK)
		m.Enabled = true

		for i, ex := range examples {
			em := m
			em.Variant = cmp.Or(ex.name, "example")

			if len(examples) > 1 && ex.name == "" {
				em.Variant += strconv.Itoa(i + 1)
			}

			em.Priority = priority(_bandExample, route)
			em.Request = routeRequest(op.Method, ex.route)
			g.contents(em, r, code, false)
		}

		// With examples, a 404 answers the other values of the parameters; without, every value answers.
		if len(examples) == 0 || notFound == nil {
			m.Priority = priority(_bandSuccess, route)
			g.contents(m, r, code, true)
		}

		if len(r.Content) > 0 && !isRange(r.Content[0].MediaType) {
			g.notAcceptable(base, r, findResponse(op.Responses, "406"))
		}
	}

	if notFound != nil && primary != nil {
		m := base
		m.Status = notFound.Status
		m.Enabled = len(examples) > 0
		m.Priority = priority(_bandNotFound, route)

		if !m.Enabled {
			m.Priority = priority(_bandDisabled, route)
		}

		g.fallback(m, g.withoutRanges(m, *notFound), http.StatusNotFound)
	}
}

// contract adds the mocks of op that refuse a request that breaks its contract, as base matches it: a 401 for a
// request without the credentials op asks for, a 415 for a body of a media type it doesn't take, and a 400 for
// parameters or a body that break their schemas, or a 422 when op declares one and no 400. Each matches what the
// openapi matcher, under not, says a request breaks.
func (g *generator) contract(op Operation, base Mock) {
	operation := cmp.Or(op.ID, op.Method+" "+base.Route)

	refuse := func(status string, band int, check string, header map[string]string) {
		m := base
		m.Status = status
		m.Priority = priority(band, base.Route)
		m.Enabled = true
		m.Request.Custom = []any{[]any{"not", []any{"openapi", map[string]any{"spec": g.spec, "operation": operation, "check": check}}}}
		g.refuse(m, findResponse(op.Responses, status), check == "request", header)
	}

	if op.Secured {
		var header map[string]string
		if len(op.Challenges) > 0 {
			header = map[string]string{"WWW-Authenticate": strings.Join(op.Challenges, ", ")}
		}

		refuse("401", _bandUnauthorized, "security", header)
	}

	if len(op.Consumes) > 0 {
		refuse("415", _bandUnsupportedMedia, "content_type", map[string]string{"Accept": strings.Join(op.Consumes, ", ")})
	}

	if op.HasInput {
		status := "400"
		if findResponse(op.Responses, "400") == nil && findResponse(op.Responses, "422") != nil {
			status = "422"
		}

		refuse(status, _bandBadRequest, "request", nil)
	}
}

// refuse adds the mocks m answers a request it refuses with: declared, the operation's response, as [generator.fallback]
// adds them, or else a problem body, which, when invalid, lists what the request breaks. Each header of header the
// response doesn't declare is added.
func (g *generator) refuse(m Mock, declared *Response, invalid bool, header map[string]string) {
	code := statusCode(m.Status, http.StatusBadRequest)

	var r Response
	if declared != nil {
		r = g.withoutRanges(m, *declared)
	}

	r.Headers = slices.Clone(r.Headers)
	for _, name := range slices.Sorted(maps.Keys(header)) {
		if !slices.ContainsFunc(r.Headers, func(h Header) bool { return strings.EqualFold(h.Name, name) }) {
			r.Headers = append(r.Headers, Header{Name: name, Examples: []Example{{Value: header[name]}}})
		}
	}

	if len(r.Content) > 0 {
		g.fallback(m, r, code)
		return
	}

	m.Reply = Reply{Status: code, Header: g.headers(m, r), Body: map[string]any{"title": http.StatusText(code), "status": code}}
	if invalid {
		m.Reply.Body, m.Reply.Template = fmt.Sprintf(_problemTemplate, http.StatusText(code), code), true
	}

	g.add(withContentType(m, _problemJSON))
}

// notAllowed adds the 405 of path, whose operations' methods and request r holds, which answers a method none of them
// has, but a HEAD when one has GET, and OPTIONS, which a CORS preflight sends. Its Allow header lists the others.
func (g *generator) notAllowed(path string, r *routeMethods) {
	allowed := slices.Clone(r.methods)
	if slices.Contains(allowed, http.MethodGet) && !slices.Contains(allowed, http.MethodHead) {
		allowed = append(allowed, http.MethodHead)
	}

	slices.Sort(allowed)

	taken := slices.Clone(allowed)
	if !slices.Contains(taken, http.MethodOptions) {
		taken = append(taken, http.MethodOptions)
		slices.Sort(taken)
	}

	methods := make([]any, len(taken))
	for i, method := range taken {
		methods[i] = method
	}

	m := Mock{Route: path, Status: "405", Name: path + ": method not allowed", Priority: priority(_bandNotAllowed, path), Enabled: true, Request: r.request}
	m.Request.Method = []any{"not", []any{"isin", methods}}
	m.Reply = Reply{
		Status: http.StatusMethodNotAllowed,
		Header: map[string]string{"Allow": strings.Join(allowed, ", ")},
		Body:   map[string]any{"title": http.StatusText(http.StatusMethodNotAllowed), "status": http.StatusMethodNotAllowed},
	}

	g.add(withContentType(m, _problemJSON))
}

// fallback adds the mocks of response r answering with status as [generator.contents] does, for a response that
// answers whatever the client accepts, such as a 404: a client whose Accept takes none of its media types gets the
// first, and a response of one media type takes any Accept.
func (g *generator) fallback(m Mock, r Response, status int) {
	start := len(g.mocks)
	g.contents(m, r, status, false)

	// The first mock contents added is that of the first media type: without extras, each media type adds one.
	first := &g.mocks[start]
	switch {
	case len(r.Content) == 1:
		first.Request.Accept = nil
	case first.Request.Accept != nil:
		offers := first.Request.Accept[2]
		first.Request.Accept = []any{"any", first.Request.Accept, []any{"notacceptable", offers}}
	}
}

// contents adds the mocks of response r answering with status: one for each of its media types, which matches the
// Accept that prefers it among the response's, or one without a body when it has none. A media range, which
// [generator.withoutRanges] leaves alone in r, matches any Accept. With extras, each example of a media type past the
// first adds a disabled mock of its own.
func (g *generator) contents(m Mock, r Response, status int, extras bool) {
	if len(r.Content) == 0 {
		m.Reply = Reply{Status: status, Header: g.headers(m, r)}
		g.add(m)

		return
	}

	offers := make([]any, len(r.Content))
	for i, c := range r.Content {
		offers[i] = c.MediaType
	}

	for _, c := range r.Content {
		cm := m
		cm.MediaType = c.MediaType

		if !isRange(c.MediaType) {
			cm.Request.Accept = []any{"accept", c.MediaType, offers}
		}

		g.body(cm, r, c, status, extras)
	}
}

// body adds the mock m of content c of response r: its body is the first example of c, or a fake value of its
// schema. With extras, each example past the first adds a disabled mock of its own. A body no value fits leaves m
// disabled and without one, with a warning.
func (g *generator) body(m Mock, r Response, c Content, status int, extras bool) {
	headers := g.headers(m, r)
	m.Reply = Reply{Status: status, Header: headers}

	switch {
	case len(c.Examples) > 0:
		g.setBody(&m, c, c.Examples[0].Value)
	case c.Schema != nil:
		v, err := fakefy.New(fakefy.WithSeed(seed(m))).Schema(g.responses.root(c.Schema))
		if err != nil {
			g.warnf("no body fits: %v: written disabled", err)
			m.Enabled = false
		} else {
			g.setBody(&m, c, v)
		}
	}

	g.add(withContentType(m, c.MediaType))

	if !extras {
		return
	}

	for i, ex := range c.Examples[min(1, len(c.Examples)):] {
		em := m
		em.Variant = cmp.Or(ex.Name, "example"+strconv.Itoa(i+2))
		em.Enabled = false
		em.Priority = priority(_bandDisabled, m.Route)
		em.Reply = Reply{Status: status, Header: headers}
		g.setBody(&em, c, ex.Value)
		g.add(withContentType(em, c.MediaType))
	}
}

// withContentType returns m with mediaType as its Content-Type, when it has a body. A media range names no type a
// client can read the body as: m stays without one.
func withContentType(m Mock, mediaType string) Mock {
	if m.Reply.Body == nil && m.Reply.BodyBase64 == "" || isRange(mediaType) {
		return m
	}

	h := make(map[string]string, len(m.Reply.Header)+1)
	maps.Copy(h, m.Reply.Header)
	h["Content-Type"] = mediaType
	m.Reply.Header = h

	return m
}

// withoutRanges returns r without its media ranges, such as */* or image/*, with a warning for each, when it has
// media types too: a client asks for those. Of a response with media ranges only, it keeps the first: its mock takes
// any Accept.
func (g *generator) withoutRanges(m Mock, r Response) Response {
	types := slices.DeleteFunc(slices.Clone(r.Content), func(c Content) bool { return isRange(c.MediaType) })
	if len(types) == len(r.Content) {
		return r
	}

	if len(types) == 0 {
		types = r.Content[:1]
	}

	for _, c := range r.Content {
		if !slices.ContainsFunc(types, func(t Content) bool { return t.MediaType == c.MediaType }) {
			g.warnf("media range %s: left out: the mocks answer %s", c.MediaType, types[0].MediaType)
		}
	}

	r.Content = types

	return r
}

// isRange reports whether mediaType is a media range, such as */* or text/*.
func isRange(mediaType string) bool {
	return strings.Contains(mediaType, "*")
}

// notAcceptable adds the 406 mock of an operation whose primary response r offers media types: the operation's own
// 406 response, in its first media type only, as its client accepts none of r's, or text listing the offers.
func (g *generator) notAcceptable(base Mock, r Response, declared *Response) {
	offers := make([]any, len(r.Content))
	names := make([]string, len(r.Content))

	for i, c := range r.Content {
		offers[i], names[i] = c.MediaType, c.MediaType
	}

	m := base
	m.Status = "406"
	m.Priority = priority(_bandNotAcceptable, base.Route)
	m.Enabled = true
	m.Request.Accept = []any{"notacceptable", offers}

	if declared != nil && len(declared.Content) > 0 {
		m.MediaType = declared.Content[0].MediaType
		g.body(m, *declared, declared.Content[0], http.StatusNotAcceptable, false)
		return
	}

	m.Reply = Reply{
		Status: http.StatusNotAcceptable,
		Header: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:   "expected one of: " + strings.Join(names, ", "),
	}

	g.add(m)
}

// headers returns the headers of response r for mock m: each header's first example, or a fake value of its schema.
func (g *generator) headers(m Mock, r Response) map[string]string {
	if len(r.Headers) == 0 {
		return nil
	}

	f := fakefy.New(fakefy.WithSeed(seed(m) ^ 1))
	h := make(map[string]string, len(r.Headers))

	for _, hd := range r.Headers {
		var v any

		switch {
		case len(hd.Examples) > 0:
			v = hd.Examples[0].Value
		case hd.Schema == nil:
			continue
		default:
			var err error
			if v, err = f.Field(hd.Name, g.responses.root(hd.Schema)); err != nil {
				g.warnf("no value of header %s fits: %v: left out", hd.Name, err)
				continue
			}
		}

		if s, ok := g.headerValue(hd, v); ok {
			h[hd.Name] = s
		}
	}

	return h
}

// view is how one side of an exchange sees the schemas of a document: a response never holds a property its schema
// marks writeOnly, and a request needn't send one it marks readOnly, though it may.
type view struct {
	marker string            // the annotation of the properties the side leaves out: writeOnly or readOnly
	keep   bool              // whether the side keeps those properties, and only stops requiring them
	doc    map[string]any    // the document's shared schemas
	defs   map[string]any    // the shared schemas as the side sees them, and copyWithout's copies
	copies map[string]string // the names in defs of copyWithout's copies, by shared schema and properties left out
}

// newView returns how a side that leaves out the properties marker marks, or with keep, only stops requiring them,
// sees doc, the shared schemas of a document.
func newView(doc map[string]any, marker string, keep bool) *view {
	vw := &view{marker: marker, keep: keep, doc: doc, defs: make(map[string]any, len(doc)), copies: make(map[string]string)}

	// Sorted, so the copies of shared schemas get the same names on every run.
	for _, name := range slices.Sorted(maps.Keys(doc)) {
		vw.defs[name] = vw.strip(doc[name], nil)
	}

	return vw
}

// RequestRoots returns each of schemas, schemas of a document whose shared schemas are defs, as a request sees it: a
// property a schema marks readOnly is no longer required, as a request needn't send it, but stays declared, as it
// may. Each is a root, as a response's is, with the shared schemas its $ref values name under $defs.
func RequestRoots(defs map[string]any, schemas []any) []map[string]any {
	vw := newView(defs, "readOnly", true)

	roots := make([]map[string]any, len(schemas))
	for i, s := range schemas {
		roots[i] = vw.root(s)
	}

	return roots
}

// root returns schema as vw's side sees it, with the document's shared schemas its $ref values name, in turn, under
// $defs, for them to resolve. Its own $defs win over a shared schema of the same name. Leaving out the others keeps
// fakefy from compiling every schema of a large spec for each value.
func (vw *view) root(schema any) map[string]any {
	root := make(map[string]any, 2)

	switch s := vw.strip(schema, nil).(type) {
	case map[string]any:
		root = s
	case bool:
		if !s {
			root["not"] = make(map[string]any)
		}
	}

	own, _ := root["$defs"].(map[string]any)
	defs := maps.Clone(own)
	if defs == nil {
		defs = make(map[string]any)
	}

	vw.named(root, defs)
	root["$defs"] = defs

	return root
}

// named adds to defs each shared schema of vw that a $ref in v names, and those that one names, in turn, but for the
// names defs holds. It reads every $ref key as one: one in data, such as an example, only adds a schema.
func (vw *view) named(v any, defs map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		ref, _ := x["$ref"].(string)
		if name, ok := defName(ref); ok {
			if def, shared := vw.defs[name]; shared {
				if _, added := defs[name]; !added {
					defs[name] = def
					vw.named(def, defs)
				}
			}
		}

		for _, val := range x {
			vw.named(val, defs)
		}
	case []any:
		for _, item := range x {
			vw.named(item, defs)
		}
	}
}

// defName returns the name of the schema under $defs that ref, a fragment such as #/$defs/Pet/properties/name, points
// into, decoded as fakefy decodes it: a URI fragment may percent-encode any character, and a JSON pointer token
// escapes / and ~.
func defName(ref string) (string, bool) {
	frag, _ := url.PathUnescape(ref) // "" for a bad escape, which names no schema

	rest, ok := strings.CutPrefix(frag, DefsPrefix)
	if !ok {
		return "", false
	}

	token, _, _ := strings.Cut(rest, "/")

	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"), true
}

// sharedName returns the name of the schema under $defs that ref names whole, decoded as defName decodes it, and
// whether it names one: #/$defs/Pet/properties/name names none.
func sharedName(ref string) (string, bool) {
	frag, _ := url.PathUnescape(ref)
	if rest, ok := strings.CutPrefix(frag, DefsPrefix); !ok || strings.Contains(rest, "/") {
		return "", false
	}

	return defName(ref)
}

// sharedRef returns the $ref of the schema named name under $defs, its name escaped as a JSON pointer token and in a
// URI fragment.
func sharedRef(name string) string {
	return DefsPrefix + url.PathEscape(strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1"))
}

// seed returns the seed of the fake values of m: the same for the same route, method, status, media type and
// variant, whatever the mock's ID or the other operations.
func seed(m Mock) uint64 {
	h := fnv.New64a()
	for _, s := range []string{m.Route, m.Method, m.Status, m.MediaType, m.Variant} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}

	return h.Sum64()
}

// primaryResponse returns the response an operation answers when it succeeds: its lowest 2xx, its 2XX, its lowest
// 3xx, or else its default. It returns nil for an operation with none of them.
func primaryResponse(responses []Response) *Response {
	var best *Response

	rank := func(status string) int {
		switch {
		case len(status) == 3 && status[0] == '2' && status != "2XX":
			return 0
		case status == "2XX":
			return 1
		case len(status) == 3 && status[0] == '3' && status != "3XX":
			return 2
		case status == "3XX":
			return 3
		case status == "default":
			return 4
		default:
			return -1
		}
	}

	for i := range responses {
		r := &responses[i]

		k := rank(r.Status)
		if k < 0 {
			continue
		}

		if best == nil || k < rank(best.Status) || k == rank(best.Status) && r.Status < best.Status {
			best = r
		}
	}

	return best
}

func findResponse(responses []Response, status string) *Response {
	for i := range responses {
		if responses[i].Status == status {
			return &responses[i]
		}
	}

	return nil
}

// statusCode returns the status code of a response key: the key's code, the start of its range, or fallback for
// default.
func statusCode(status string, fallback int) int {
	if len(status) == 3 && strings.HasSuffix(status, "XX") {
		return int(status[0]-'0') * 100
	}

	if n, err := strconv.Atoi(status); err == nil {
		return n
	}

	return fallback
}

// pathExample is a concrete path of an operation, from examples of its path parameters, and the name of the example.
type pathExample struct {
	path  string // as the client writes it, decoded: the path, or the value of one parameter
	name  string
	route *pathtemplate.Route // the route of the path
}

// pathExamples returns the concrete paths of op that examples of its path parameters give: the nth path takes the
// nth example of each parameter, or its last when it has fewer. An operation with a parameter without examples a
// client can write has none.
func (g *generator) pathExamples(op Operation) []pathExample {
	if len(op.PathParams) == 0 {
		return nil
	}

	// The examples of each parameter as a client writes them in the path, and their names.
	written := make([][]pathExample, len(op.PathParams))

	n := 0
	for i, p := range op.PathParams {
		for _, ex := range p.Examples {
			if v, ok := g.paramValue(p, ex.Value); ok {
				written[i] = append(written[i], pathExample{path: v, name: ex.Name})
			}
		}

		if len(written[i]) == 0 {
			return nil
		}

		n = max(n, len(written[i]))
	}

	out := make([]pathExample, 0, n)
	for i := range n {
		var ex pathExample

		values := make(map[string]string, len(op.PathParams))
		for j, p := range op.PathParams {
			w := written[j][min(i, len(written[j])-1)]
			values["{"+p.Name+"}"] = w.path

			if len(op.PathParams) == 1 {
				ex.name = w.name
			}
		}

		// One pass over the template: a value that holds {name} is a value, not a parameter to fill.
		ex.path = pathtemplate.Fill(op.Path, pathtemplate.Decode, func(param string) string {
			if v, ok := values[param]; ok {
				return v
			}

			return param
		})

		// A request to a path its operation's route doesn't take would get none of the operation's checks.
		route, ok := pathtemplate.NewExample(op.BasePath, op.Path, values)
		if !ok {
			g.warnf("path %s of the examples of its parameters: left out: a value is empty, or holds a / inside a segment, which the route doesn't take", ex.path)
			continue
		}

		ex.route = route
		out = append(out, ex)
	}

	return out
}

// paramValue returns v, a value of path parameter p, as a client writes it in the path: in its media type, as
// [generator.mediaValue] writes it, or in its style.
func (g *generator) paramValue(p Parameter, v any) (string, bool) {
	if p.MediaType == "" {
		return pathValue(p, v), true
	}

	return g.mediaValue("path parameter "+p.Name, p.MediaType, v)
}

// pathValue returns v, a value of parameter p, as a client writes it in a path, in the style of p, as RFC 6570, which
// OpenAPI follows, does: 7 as 7, .7 or ;id=7. An object's keys come in order, as JSON has none.
func pathValue(p Parameter, v any) string {
	// prefix starts the value, sep joins its items, and named names each item in matrix style.
	prefix, sep, named := "", ",", false

	switch p.Style {
	case "label":
		prefix = "."
		if p.Explode {
			sep = "."
		}
	case "matrix":
		prefix, named = ";", true
		if p.Explode {
			sep = ";"
		}
	}

	item := func(name, value string) string {
		if !named {
			return value
		}

		if value == "" {
			return name
		}

		return name + "=" + value
	}

	var items []string

	switch x := v.(type) {
	case []any:
		for _, e := range x {
			items = append(items, text(e))
		}

		if !p.Explode {
			return prefix + item(p.Name, strings.Join(items, ","))
		}

		for i, e := range items {
			items[i] = item(p.Name, e)
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			if p.Explode {
				items = append(items, k+"="+text(x[k]))
			} else {
				items = append(items, k, text(x[k]))
			}
		}

		if !p.Explode {
			return prefix + item(p.Name, strings.Join(items, ","))
		}
	default:
		return prefix + item(p.Name, text(v))
	}

	return prefix + strings.Join(items, sep)
}

// priority returns the priority of a mock of route in band: every mock of a route answers before those of a route
// with more templated segments, as OpenAPI matches /pets/mine before /pets/{id}, whatever their bands.
func priority(band int, route string) int {
	return pathtemplate.Templated(route)*_routeStep + band
}

// routeRequest returns what a mock of rt, of method, matches. A glob would read a * or [ of the path as its own.
func routeRequest(method string, rt *pathtemplate.Route) Request {
	r := Request{Method: method}

	switch rt.Kind {
	case pathtemplate.Literal:
		r.Path = []any{"equal", rt.Path}
	case pathtemplate.Segments:
		r.PathTemplate = rt.Path
	default:
		r.Path = []any{"regex", rt.Path}
	}

	return r
}

// text returns v, a value of the spec or a fake one, which JSON holds, as text: a string as is, anything else as
// JSON.
func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	b, _ := json.Marshal(v, json.Deterministic(true))

	return string(b)
}

// headerValue returns v as the value of header hd: in its media type, as [generator.mediaValue] writes it, or in
// OpenAPI's simple style.
func (g *generator) headerValue(hd Header, v any) (string, bool) {
	if hd.MediaType == "" {
		return pathValue(Parameter{Name: hd.Name, Style: "simple", Explode: hd.Explode}, v), true
	}

	return g.mediaValue("header "+hd.Name, hd.MediaType, v)
}

// mediaValue returns v, the value of what, such as header X-Rate, in mediaType, as setBody writes a body. A list or an
// object in a media type that isn't JSON is left out, with a warning.
func (g *generator) mediaValue(what, mediaType string, v any) (string, bool) {
	if IsJSON(mediaType) {
		b, _ := json.Marshal(v, json.Deterministic(true))
		return string(b), true
	}

	switch v.(type) {
	case map[string]any, []any:
		g.warnf("%s is data, not text: left out: give a string example", what)
		return "", false
	}

	return text(v), true
}

// strip returns a copy of the schema v without the properties vw's side leaves out, at any depth, nor their names in
// required, or, when it keeps them, only without their names in required. The schemas sameObject has for a schema
// describe one object: a property any of them marks is left out of each, as are dropped, those the schemas v applies
// with mark. A shared schema is applied as copyWithout has it. The branches of anyOf and oneOf, which may not apply,
// and the object a property holds keep their own.
func (vw *view) strip(v any, dropped []string) any {
	switch x := v.(type) {
	case map[string]any:
		dropped = slices.Concat(dropped, markedProperties(x, vw.doc, vw.marker))
		out := make(map[string]any, len(x))

		for k, val := range x {
			named, isMap := val.(map[string]any)

			switch {
			case slices.Contains(ValueKeywords, k):
				// Data, whatever keys it holds.
				out[k] = val
			case k == "properties" && isMap && !vw.keep:
				kept := make(map[string]any, len(named))

				for name, p := range named {
					if !slices.Contains(dropped, name) {
						kept[name] = vw.strip(p, nil)
					}
				}

				out[k] = kept
			case slices.Contains(SchemaMapKeywords, k) && isMap:
				// Names, not keywords: a schema named const is a schema.
				each := make(map[string]any, len(named))
				for name, p := range named {
					each[name] = vw.strip(p, nil)
				}

				out[k] = each
			case k == "allOf":
				out[k] = vw.strip(val, dropped)
			default:
				out[k] = vw.strip(val, nil)
			}
		}

		if req, ok := out["required"].([]any); ok && len(dropped) > 0 {
			out["required"] = slices.DeleteFunc(slices.Clone(req), func(r any) bool {
				s, _ := r.(string)
				return slices.Contains(dropped, s)
			})
		}

		ref, _ := x["$ref"].(string)
		if name, ok := sharedName(ref); ok {
			if copied := vw.copyWithout(name, dropped); copied != name {
				out["$ref"] = sharedRef(copied)
			}
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = vw.strip(item, dropped)
		}

		return out
	default:
		return v
	}
}

// copyWithout returns the name, in vw's shared schemas, of the document's shared schema name as strip has it without
// the properties of dropped, or name when changes finds it the same. Each copy is made once, so one that holds itself
// refers to itself.
func (vw *view) copyWithout(name string, dropped []string) string {
	def, _ := vw.doc[name].(map[string]any)

	var without []string

	for _, s := range sameObject(def, vw.doc) {
		for _, d := range dropped {
			if vw.changes(s, d) && !slices.Contains(without, d) {
				without = append(without, d)
			}
		}
	}

	if len(without) == 0 {
		return name
	}

	slices.Sort(without)

	key := name + "\x00" + strings.Join(without, "\x00")
	if copied, ok := vw.copies[key]; ok {
		return copied
	}

	copied := name
	for i := 2; ; i++ {
		copied = name + "_" + strconv.Itoa(i)

		_, shared := vw.doc[copied]
		if _, made := vw.defs[copied]; !shared && !made {
			break
		}
	}

	// Taken before the copy is made, for the copy to refer to itself.
	vw.copies[key] = copied
	vw.defs[copied] = nil
	vw.defs[copied] = vw.strip(def, without)

	return copied
}

// sameObject returns schema s and the schemas that describe the same object with it, each once: those of its allOf,
// and the schema of defs its $ref names, in turn.
func sameObject(s, defs map[string]any) []map[string]any {
	var out []map[string]any

	followed := make(map[string]bool)

	var walk func(s map[string]any)
	walk = func(s map[string]any) {
		out = append(out, s)

		all, _ := s["allOf"].([]any)
		for _, b := range all {
			branch, _ := b.(map[string]any)
			walk(branch)
		}

		ref, _ := s["$ref"].(string)
		if name, ok := sharedName(ref); ok && !followed[name] {
			followed[name] = true

			def, _ := defs[name].(map[string]any)
			walk(def)
		}
	}

	walk(s)

	return out
}

// markedProperties returns the names of the properties that schema s, or a schema sameObject has for it, marks with
// marker, as marked decides.
func markedProperties(s, defs map[string]any, marker string) []string {
	var names []string

	for _, o := range sameObject(s, defs) {
		props, _ := o["properties"].(map[string]any)
		for name, p := range props {
			if marked(p, defs, marker, nil) {
				names = append(names, name)
			}
		}
	}

	return names
}

// changes reports whether vw's side sees schema s other than it is without the property name: s requires it, or
// declares it, when the side leaves it out.
func (vw *view) changes(s map[string]any, name string) bool {
	props, _ := s["properties"].(map[string]any)
	required, _ := s["required"].([]any)
	_, declared := props[name]

	return slices.Contains(required, any(name)) || declared && !vw.keep
}

// marked reports whether schema v is marked with marker, such as writeOnly, or refers to a schema of defs that is, or
// allOf applies one to it. seen holds the names of the schemas the references followed so far, which a cycle comes
// back to.
func marked(v any, defs map[string]any, marker string, seen []string) bool {
	s, _ := v.(map[string]any)
	if s[marker] == true {
		return true
	}

	all, _ := s["allOf"].([]any)
	if slices.ContainsFunc(all, func(b any) bool { return marked(b, defs, marker, seen) }) {
		return true
	}

	ref, _ := s["$ref"].(string)

	name, ok := sharedName(ref)
	if !ok || slices.Contains(seen, name) {
		return false
	}

	return marked(defs[name], defs, marker, append(seen, name))
}
