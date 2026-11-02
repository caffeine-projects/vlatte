package openapi

import (
	"cmp"
	json "encoding/json/v2"
	"fmt"
	"hash/fnv"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte/fakefy"
)

// The priority bands of generated mocks, which the number of templated segments of their path adds to.
const (
	_bandDisabled      = 100
	_bandExample       = 200
	_bandSuccess       = 300
	_bandNotAcceptable = 400
	_bandNotFound      = 500
)

// _contractStatuses are the statuses a request's contract decides, which no generated mock answers.
var _contractStatuses = []string{"400", "401", "405", "415", "422"}

// Mock is a generated mock: the route and response it answers, and the request it matches.
type Mock struct {
	// ID is the mock's ID, which its file is named after. [Generate] sets it with [RouteFirst].
	ID string

	// Route is the path the mock answers, under the document's base path, as the spec writes it.
	Route string

	// Method is the HTTP method, in upper case.
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
	// Method is the HTTP method.
	Method string

	// Path is what the path matches, as a mock file's path key takes it: the route as a glob, for a route without
	// parameters; an equal matcher list, for an example of one; or a regex matcher list, for a route with a parameter
	// inside a segment. It is nil for a PathTemplate.
	Path any

	// PathTemplate is the path template to match, for a route whose parameters are whole segments.
	PathTemplate string

	// Accept is the matcher spec of the Accept header, or nil for none.
	Accept []any
}

// Reply is what a generated mock answers.
type Reply struct {
	// Status is the status code.
	Status int

	// Header holds the headers, by name.
	Header map[string]string

	// Body is the body, as a mock file's body key takes it: text as is, anything else as JSON. nil is no body.
	Body any
}

// Output is what [Generate] makes of a document.
type Output struct {
	// Files are the mock files to write.
	Files []File

	// Warnings are what the generator could not honor, such as a schema no fake value fits.
	Warnings []string
}

// Generate makes the mock files of doc: mocks named by [RouteFirst], each in a file of its own, as [PerMock] lays
// them out. A body no value fits, by its examples or its schema, leaves its mock without one and disabled, with a
// warning.
func Generate(doc *Document) *Output {
	g := generator{doc: doc, defs: make(map[string]any, len(doc.Defs))}
	for name, def := range doc.Defs {
		g.defs[name] = stripWriteOnly(def)
	}

	ops := slices.Clone(doc.Operations)
	slices.SortFunc(ops, func(a, b Operation) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Method, b.Method))
	})

	for _, op := range ops {
		g.operation(op)
	}

	name(g.mocks, RouteFirst)

	return &Output{Files: PerMock(g.mocks), Warnings: g.warnings}
}

// generator holds the mocks and warnings of a document as they are made.
type generator struct {
	doc      *Document
	defs     map[string]any
	mocks    []Mock
	warnings []string
}

// operation makes the mocks of op.
func (g *generator) operation(op Operation) {
	route := joinPath(g.doc.BasePath, op.Path)
	segments := templated(route)
	request := routeRequest(op.Method, route)

	primary := primaryResponse(op.Responses)
	notFound := findResponse(op.Responses, "404")
	examples := pathExamples(op)
	base := Mock{Route: route, Method: op.Method, Name: cmp.Or(op.Summary, op.Method+" "+route), Request: request}

	for _, r := range op.Responses {
		if slices.Contains(_contractStatuses, r.Status) || r.Status == "406" || r.Status == "404" && primary != nil {
			continue
		}

		m := base
		m.Status = r.Status
		r = g.withoutRanges(m, r)

		if primary == nil || r.Status != primary.Status {
			m.Priority = _bandDisabled + segments
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

			em.Priority = _bandExample + segments
			// The server compares the path decoded, and a glob would read an example's * or [ as its own.
			em.Request = Request{Method: op.Method, Path: []any{"equal", joinPath(g.doc.BasePath, ex.path)}}
			g.contents(em, r, code, false)
		}

		// With examples, a 404 answers the other values of the parameters; without, every value answers.
		if len(examples) == 0 || notFound == nil {
			m.Priority = _bandSuccess + segments
			g.contents(m, r, code, true)
		}

		if len(r.Content) > 0 && !isRange(r.Content[0].MediaType) {
			g.notAcceptable(base, r, findResponse(op.Responses, "406"), segments)
		}
	}

	if notFound != nil && primary != nil {
		m := base
		m.Status = notFound.Status
		m.Enabled = len(examples) > 0
		m.Priority = _bandNotFound + segments

		if !m.Enabled {
			m.Priority = _bandDisabled + segments
		}

		g.first(m, *notFound, http.StatusNotFound)
	}
}

// contents adds the mocks of response r answering with status: one for each of its media types, which matches the
// Accept that prefers it among the response's, or one without a body when it has none. A media range, which
// [generator.withoutRanges] leaves alone in r, matches any Accept. With extras, each example of a media type past the
// first adds a disabled mock of its own.
func (g *generator) contents(m Mock, r Response, status int, extras bool) {
	if len(r.Content) == 0 {
		m.Reply = Reply{Status: status, Header: g.headers(m, r)}
		g.mocks = append(g.mocks, m)

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

// first adds the mock of response r answering with status, with the body of its first media type, if any.
func (g *generator) first(m Mock, r Response, status int) {
	if len(r.Content) == 0 {
		m.Reply = Reply{Status: status, Header: g.headers(m, r)}
		g.mocks = append(g.mocks, m)

		return
	}

	m.MediaType = r.Content[0].MediaType
	g.body(m, r, r.Content[0], status, false)
}

// body adds the mock m of content c of response r: its body is the first example of c, or a fake value of its
// schema. With extras, each example past the first adds a disabled mock of its own. A body no value fits leaves m
// disabled and without one, with a warning.
func (g *generator) body(m Mock, r Response, c Content, status int, extras bool) {
	m.Reply = Reply{Status: status, Header: g.headers(m, r)}

	switch {
	case len(c.Examples) > 0:
		m.Reply.Body = encodeBody(c.MediaType, c.Examples[0].Value, c.Schema)
	case c.Schema != nil:
		v, err := fakefy.New(fakefy.WithSeed(seed(m))).Schema(g.root(c.Schema))
		if err != nil {
			g.warnf(m, "no body fits: %v: written disabled", err)
			m.Enabled = false
		} else {
			m.Reply.Body = encodeBody(c.MediaType, v, c.Schema)
		}
	}

	// A media range names no type a client can read the body as.
	if m.Reply.Body != nil && !isRange(c.MediaType) {
		if m.Reply.Header == nil {
			m.Reply.Header = make(map[string]string, 1)
		}

		m.Reply.Header["Content-Type"] = c.MediaType
	}

	g.mocks = append(g.mocks, m)

	if !extras {
		return
	}

	for i, ex := range c.Examples[min(1, len(c.Examples)):] {
		em := m
		em.Variant = cmp.Or(ex.Name, "example"+strconv.Itoa(i+2))
		em.Enabled = false
		em.Priority = _bandDisabled + templated(m.Route)
		em.Reply.Body = encodeBody(c.MediaType, ex.Value, c.Schema)
		g.mocks = append(g.mocks, em)
	}
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
			g.warnf(m, "media range %s: left out: the mocks answer %s", c.MediaType, types[0].MediaType)
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
// 406 response, or text listing the offers.
func (g *generator) notAcceptable(base Mock, r Response, declared *Response, segments int) {
	offers := make([]any, len(r.Content))
	names := make([]string, len(r.Content))

	for i, c := range r.Content {
		offers[i], names[i] = c.MediaType, c.MediaType
	}

	m := base
	m.Status = "406"
	m.Priority = _bandNotAcceptable + segments
	m.Enabled = true
	m.Request.Accept = []any{"notacceptable", offers}

	if declared != nil && len(declared.Content) > 0 {
		g.first(m, *declared, http.StatusNotAcceptable)
		return
	}

	m.Reply = Reply{
		Status: http.StatusNotAcceptable,
		Header: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:   "expected one of: " + strings.Join(names, ", "),
	}

	g.mocks = append(g.mocks, m)
}

// headers returns the headers of response r for mock m: each header's first example, or a fake value of its schema.
func (g *generator) headers(m Mock, r Response) map[string]string {
	if len(r.Headers) == 0 {
		return nil
	}

	f := fakefy.New(fakefy.WithSeed(seed(m) ^ 1))
	h := make(map[string]string, len(r.Headers))

	for _, hd := range r.Headers {
		if len(hd.Examples) > 0 {
			h[hd.Name] = headerValue(hd.Examples[0].Value)
			continue
		}

		if hd.Schema == nil {
			continue
		}

		v, err := f.Field(hd.Name, g.root(hd.Schema))
		if err != nil {
			g.warnf(m, "no value of header %s fits: %v: left out", hd.Name, err)
			continue
		}

		h[hd.Name] = headerValue(v)
	}

	return h
}

// root returns schema, without its writeOnly properties, with the document's shared schemas under $defs, for its
// $ref values to resolve. Its own $defs win over a shared schema of the same name.
func (g *generator) root(schema any) map[string]any {
	root := make(map[string]any, 2)

	switch s := stripWriteOnly(schema).(type) {
	case map[string]any:
		root = s
	case bool:
		if !s {
			root["not"] = make(map[string]any)
		}
	}

	defs := maps.Clone(g.defs)
	own, _ := root["$defs"].(map[string]any)
	maps.Copy(defs, own)
	root["$defs"] = defs

	return root
}

func (g *generator) warnf(m Mock, format string, args ...any) {
	where := strings.Join(slices.DeleteFunc([]string{m.Method, m.Route, m.Status, m.MediaType, m.Variant}, func(s string) bool {
		return s == ""
	}), " ")
	g.warnings = append(g.warnings, where+": "+fmt.Sprintf(format, args...))
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
	path string
	name string
}

// pathExamples returns the concrete paths of op that examples of its path parameters give: the nth path takes the
// nth example of each parameter, or its first when it has fewer. An operation with a parameter without examples has
// none.
func pathExamples(op Operation) []pathExample {
	if len(op.PathParams) == 0 {
		return nil
	}

	n := 0
	for _, p := range op.PathParams {
		if len(p.Examples) == 0 {
			return nil
		}

		n = max(n, len(p.Examples))
	}

	out := make([]pathExample, n)
	for i := range n {
		path := op.Path

		for _, p := range op.PathParams {
			ex := p.Examples[min(i, len(p.Examples)-1)]
			path = strings.ReplaceAll(path, "{"+p.Name+"}", text(ex.Value))

			if len(op.PathParams) == 1 {
				out[i].name = ex.Name
			}
		}

		out[i].path = path
	}

	return out
}

// _param matches a parameter of a path, such as {id}.
var _param = regexp.MustCompile(`\{[^{}/]+\}`)

// templated returns the number of segments of path that hold a parameter.
func templated(path string) int {
	n := 0
	for seg := range strings.SplitSeq(path, "/") {
		if _param.MatchString(seg) {
			n++
		}
	}

	return n
}

// _wholeParam matches a segment that is a parameter, such as {id}.
var _wholeParam = regexp.MustCompile(`^\{[^{}/]+\}$`)

// routeRequest returns what a mock of route matches: its exact path, a path template whose parameters are whole
// segments, or a regular expression of the path for a parameter inside a segment.
func routeRequest(method, route string) Request {
	r := Request{Method: method}

	if !_param.MatchString(route) {
		r.Path = route
		return r
	}

	whole := true
	for seg := range strings.SplitSeq(route, "/") {
		if strings.ContainsAny(seg, "{}") && !_wholeParam.MatchString(seg) {
			whole = false
		}
	}

	if whole {
		r.PathTemplate = route
		return r
	}

	var b strings.Builder

	b.WriteByte('^')

	last := 0
	for _, loc := range _param.FindAllStringIndex(route, -1) {
		b.WriteString(regexp.QuoteMeta(route[last:loc[0]]))
		b.WriteString(`[^/]+`)

		last = loc[1]
	}

	b.WriteString(regexp.QuoteMeta(route[last:]))
	b.WriteByte('$')
	r.Path = []any{"regex", b.String()}

	return r
}

// joinPath joins a base path and a path of the spec.
func joinPath(base, path string) string {
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return path
	}

	if path == "/" {
		return base
	}

	return base + path
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

// headerValue returns v as a header value: a list as its items joined by commas, as OpenAPI's simple style writes it.
func headerValue(v any) string {
	if list, ok := v.([]any); ok {
		items := make([]string, len(list))
		for i, item := range list {
			items[i] = text(item)
		}

		return strings.Join(items, ",")
	}

	return text(v)
}

// stripWriteOnly returns a copy of the schema v without its writeOnly properties, which a response never holds, at
// any depth.
func stripWriteOnly(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))

		var dropped []string

		for k, val := range x {
			if k == "properties" {
				props, ok := val.(map[string]any)
				if ok {
					kept := make(map[string]any, len(props))

					for name, p := range props {
						if pm, ok := p.(map[string]any); ok && pm["writeOnly"] == true {
							dropped = append(dropped, name)
							continue
						}

						kept[name] = stripWriteOnly(p)
					}

					out[k] = kept

					continue
				}
			}

			out[k] = stripWriteOnly(val)
		}

		if req, ok := out["required"].([]any); ok && len(dropped) > 0 {
			out["required"] = slices.DeleteFunc(slices.Clone(req), func(r any) bool {
				s, _ := r.(string)
				return slices.Contains(dropped, s)
			})
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = stripWriteOnly(item)
		}

		return out
	default:
		return v
	}
}
