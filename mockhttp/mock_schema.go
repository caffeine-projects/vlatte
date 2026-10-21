package mockhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/bodyfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/filterbuilder"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/match"
)

// The mock file format is declared here, one table per object of a mock file. The tables decode a document
// into the *Def types below; mockDoc.build checks them, then applies them to a Builder.

type (
	mockField        = config.Field[mockDef]
	filePatternField = config.Field[filePatternDef]
	basicAuthField   = config.Field[basicAuthDef]
	replyField       = config.Field[replyDef]
	sequenceField    = config.Field[sequenceDef]
	randomField      = config.Field[randomDef]
	proxyField       = config.Field[proxyDef]
	eventField       = config.Field[vlatte.EventConfig]
)

// mockDef is a decoded mock file: what its keys set, before it is applied to a Builder.
type mockDef struct {
	id          string
	name        string
	priority    int
	hasPriority bool
	enabled     bool
	delay       time.Duration
	hasDelay    bool
	repeat      int64
	scenario    scenarioDef
	events      []vlatte.EventConfig
	request     requestDef
	reply       *replyDef
	sequence    *sequenceDef
	random      *randomDef
	proxy       *proxyDef
	proxyAll    *proxyDef
	ext         map[string]any
	dir         string // where relative files open from: the mock file's directory; "" is the working directory
}

type scenarioDef struct {
	set           bool
	name          string
	requiredState string
	newState      string
}

// requestDef holds the request matchers, as Builder calls, and the keys that set them.
type requestDef struct {
	keys      []string
	scheme    func(*Builder)
	method    func(*Builder)
	host      func(*Builder)
	clientIP  func(*Builder)
	basicAuth func(*Builder)
	url       map[string]func(*Builder) // by key: url, url_match or path; only one may be set
	fragment  func(*Builder)
	query     []func(*Builder)
	queries   []func(*Builder)
	header    []func(*Builder)
	headers   []func(*Builder)
	cookie    []func(*Builder)
	form      []func(*Builder)
	forms     []func(*Builder)
	formFile  []func(*Builder)
	body      func(*Builder)

	pathTemplate   *pathTemplate // the parsed path_template, which adds to the matcher of url, url_match or path
	pathParams     []func(*Builder)
	pathParamNames []string // the names under path_params, sorted
}

// basicAuthDef is a decoded request.basic_auth: the matchers of its username and password.
type basicAuthDef struct {
	username vlatte.Matcher
	password vlatte.Matcher
}

// filePatternDef is a decoded form_file pattern.
type filePatternDef struct {
	pattern  *FilePattern
	hasBody  bool
	bodyFile string
}

type replyDef struct {
	status         int
	header         []headerValue
	headerTemplate []headerValue
	trailer        []headerValue
	body           any
	hasBody        bool
	bodyFile       string
	templated      bool
	data           any
	hasData        bool
	gzip           bool
	multipart      *mpart.Spec
}

type headerValue struct {
	name  string
	value string
}

type sequenceDef struct {
	replies    []*replyDef
	afterEnded *replyDef
	set        bool // responses was set
}

type randomDef struct {
	replies []*replyDef
	seed    int64
	seeded  bool
	set     bool // responses was set
}

type proxyDef struct {
	target               string
	requestHeader        []headerValue
	header               []headerValue
	removeRequestHeaders []string
	trimPrefix           string
	trimSuffix           string
	timeout              time.Duration
	hasTimeout           bool
	sslVerify            bool
	hasSSLVerify         bool
	hasTarget            bool
	xForwarded           bool
	preserveHost         bool
}

// _responseKeys are the ways to reply, in the order conflicts are reported.
var _responseKeys = []string{"response", "response_sequence", "response_random", "proxy", "proxy_all"}

// _urlKeys are the ways to match the URL, in the order conflicts are reported.
var _urlKeys = []string{"url", "url_match", "path"}

var _mockSchema = config.NewSchema(
	mockField{
		Key: "id", Kind: config.String, Usage: "ID of the mock, in place of a generated one: letters, digits, '.', '_', '~' and '-'.",
		Set: func(d *mockDef, v config.Value) error { d.id = v.Str; return checkMockID(v.Str) },
	},
	mockField{
		Key: "name", Kind: config.String, Usage: "Name of the mock, shown in logs.",
		Set: func(d *mockDef, v config.Value) error { d.name = v.Str; return nil },
	},
	mockField{
		Key: "priority", Kind: config.Int, Usage: "Mocks with a lower priority are matched first.",
		Set: func(d *mockDef, v config.Value) error { d.priority, d.hasPriority = int(v.Int), true; return nil },
	},
	mockField{
		Key: "enabled", Kind: config.Bool, Default: "true", Usage: "Whether the mock can match requests.",
		Set: func(d *mockDef, v config.Value) error { d.enabled = v.Bool; return nil },
	},
	mockField{
		Key: "delay", Kind: config.Duration, Usage: "How long to wait before replying: milliseconds or a Go duration (1.5s).",
		Set: func(d *mockDef, v config.Value) error { d.delay, d.hasDelay = v.Dur, true; return nil },
	},
	mockField{
		Key: "repeat", Kind: config.Int, Usage: "How many times the mock can match.",
		Set: func(d *mockDef, v config.Value) error { d.repeat = v.Int; return checkMin(v.Int, 1) },
	},
	mockField{
		Key: "scenario.name", Kind: config.String, Usage: "Scenario the mock belongs to.",
		Set: func(d *mockDef, v config.Value) error { d.scenario.set, d.scenario.name = true, v.Str; return nil },
	},
	mockField{
		Key: "scenario.required_state", Kind: config.String, Usage: "State the scenario must be in for the mock to match.",
		Set: func(d *mockDef, v config.Value) error {
			d.scenario.set, d.scenario.requiredState = true, v.Str
			return nil
		},
	},
	mockField{
		Key: "scenario.new_state", Kind: config.String, Usage: "State the scenario moves to after the mock matches.",
		Set: func(d *mockDef, v config.Value) error { d.scenario.set, d.scenario.newState = true, v.Str; return nil },
	},
	mockField{
		Key: "on_complete", Kind: config.Any, Usage: "Events to run after the mock replies: a list of {name, parameters}.",
		Set: func(d *mockDef, v config.Value) error { return setEvents(&d.events, d.dir, v) },
	},
	mockField{
		Key: "request.scheme", Kind: config.Any, Usage: "Scheme to match: text, or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "scheme")
			return matchText(v, func(s string) { d.request.scheme = func(b *Builder) { b.Scheme(s) } },
				func(m vlatte.Matcher) { d.request.scheme = func(b *Builder) { b.SchemeMatches(m) } })
		},
	},
	mockField{
		Key: "request.method", Kind: config.Any, Usage: "Method to match: text, methods separated by commas, or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "method")
			return setMethod(&d.request, v)
		},
	},
	mockField{
		Key: "request.host", Kind: config.Any, Usage: "Host the request names, without its port: text, compared ignoring case, or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "host")
			return setHost(&d.request, v)
		},
	},
	mockField{
		Key: "request.client_ip", Kind: config.Any, Usage: "Address of the client, without its port: text or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "client_ip")
			if s, isText := v.Any.(string); isText {
				if err := bareHost(v, s); err != nil {
					return err
				}
			}

			m, err := matcherOf(v)
			if err != nil {
				return err
			}

			d.request.clientIP = func(b *Builder) { b.ClientIP(m) }

			return nil
		},
	},
	mockField{
		Key: "request.url", Kind: config.Any,
		Usage: "URL to match: an absolute URL, compared ignoring case; a relative one, such as /items?page=2, " +
			"whose path and query must be the same, the parameters in any order, each with its values in order; or a matcher list.",
		Set: func(d *mockDef, v config.Value) error { return setURL(&d.request, v) },
	},
	mockField{
		Key: "request.url_match", Kind: config.String, Usage: "Regular expression the full URL must match.",
		Set: func(d *mockDef, v config.Value) error { return setURLMatch(&d.request, v) },
	},
	mockField{
		Key: "request.path", Kind: config.Any, Usage: "Path to match: a glob, or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "path")
			d.request.url = withURL(d.request.url)

			return matchText(v, func(s string) { d.request.url["path"] = func(b *Builder) { b.URL(match.URLPath(s)) } },
				func(m vlatte.Matcher) { d.request.url["path"] = func(b *Builder) { b.URLPath(m) } })
		},
	},
	mockField{
		Key: "request.path_template", Kind: config.String,
		Usage: "Path to match with parameters, such as /users/{id}: a parameter is a whole segment, and takes any segment but an empty one.",
		Set:   func(d *mockDef, v config.Value) error { return setPathTemplate(&d.request, v) },
	},
	mockField{
		Key: "request.path_params", Kind: config.Any, Usage: "Parameters of path_template to match, by name: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "path_params")
			if obj, ok := v.Any.(map[string]any); ok {
				d.request.pathParamNames = slices.Sorted(maps.Keys(obj))
			}

			return matchEach(v, &d.request.pathParams, func(b *Builder, name string, m vlatte.Matcher) { b.PathParam(name, m) })
		},
	},
	mockField{
		Key: "request.fragment", Kind: config.Any,
		Usage: `URL fragment to match: a glob, or a matcher list. Only clients that write a "#" into the request target send one.`,
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "fragment")

			return matchText(v, func(s string) { d.request.fragment = func(b *Builder) { b.URLFragment(match.GlobMatch(s)) } },
				func(m vlatte.Matcher) { d.request.fragment = func(b *Builder) { b.URLFragment(m) } })
		},
	},
	mockField{
		Key: "request.query", Kind: config.Any, Usage: "Query parameters to match, by name: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "query")
			return matchEach(v, &d.request.query, func(b *Builder, name string, m vlatte.Matcher) { b.Query(name, m) })
		},
	},
	mockField{
		Key: "request.queries", Kind: config.Any, Usage: "Query parameters with many values to match, by name.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "queries")
			return matchEach(v, &d.request.queries, func(b *Builder, name string, m vlatte.Matcher) { b.Queries(name, m) })
		},
	},
	mockField{
		Key: "request.header", Kind: config.Any, Usage: "Headers to match, by name: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "header")
			if err := sameHeader(v); err != nil {
				return err
			}

			return matchEach(v, &d.request.header, func(b *Builder, name string, m vlatte.Matcher) { b.Header(name, m) })
		},
	},
	mockField{
		Key: "request.headers", Kind: config.Any, Usage: "Headers with many values to match, by name: every value, as a list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "headers")
			if err := sameHeader(v); err != nil {
				return err
			}

			return matchLists(v, &d.request.headers, func(b *Builder, name string, m vlatte.Matcher) { b.Headers(name, m) })
		},
	},
	mockField{
		Key: "request.cookie", Kind: config.Any, Usage: "Cookies to match, by name, which keeps its case: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "cookie")
			return matchEach(v, &d.request.cookie, func(b *Builder, name string, m vlatte.Matcher) { b.Cookie(name, m) })
		},
	},
	mockField{
		Key: "request.basic_auth", Kind: config.Any, Usage: "Credentials of a Basic Authorization header to match: username and password.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "basic_auth")

			var auth basicAuthDef

			err := _basicAuthSchema.Decode(&auth, v)
			err = withRequired(err, auth.username != nil, v, "username")
			err = withRequired(err, auth.password != nil, v, "password")
			if err != nil {
				return err
			}

			d.request.basicAuth = func(b *Builder) { b.BasicAuthMatches(auth.username, auth.password) }

			return nil
		},
	},
	mockField{
		Key: "request.form", Kind: config.Any, Usage: "Fields of a urlencoded or multipart form body to match, by name: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "form")
			return matchEach(v, &d.request.form, func(b *Builder, name string, m vlatte.Matcher) { b.FormField(name, m) })
		},
	},
	mockField{
		Key: "request.forms", Kind: config.Any, Usage: "Fields of a form body with many values to match, by name: every value, as a list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "forms")
			return matchLists(v, &d.request.forms, func(b *Builder, name string, m vlatte.Matcher) { b.FormFields(name, m) })
		},
	},
	mockField{
		Key: "request.form_file", Kind: config.Any,
		Usage: "Files of a multipart form body to match, by field name: a pattern, or a list of patterns, each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "form_file")
			return decodeFormFiles(d, v)
		},
	},
	mockField{
		Key: "request.body", Kind: config.Any, Usage: "Body to match: text or a matcher list.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "body")

			m, err := matcherOf(v)
			if err != nil {
				return err
			}

			d.request.body = func(b *Builder) { b.Body(m) }

			return nil
		},
	},
	mockField{
		Key: "response", Kind: config.Any, Usage: "Reply: status, header, body and more.",
		Set: func(d *mockDef, v config.Value) error {
			d.reply = newReplyDef()
			return decodeReply(d.reply, v)
		},
	},
	mockField{
		Key: "response_sequence", Kind: config.Any, Usage: "Replies served in order: responses, and after_ended once they run out.",
		Set: func(d *mockDef, v config.Value) error {
			d.sequence = new(sequenceDef)
			return withRequired(_sequenceSchema.Decode(d.sequence, v), d.sequence.set, v, "responses")
		},
	},
	mockField{
		Key: "response_random", Kind: config.Any, Usage: "Replies served at random: responses, and a seed to repeat the order.",
		Set: func(d *mockDef, v config.Value) error {
			d.random = new(randomDef)
			return withRequired(_randomSchema.Decode(d.random, v), d.random.set, v, "responses")
		},
	},
	mockField{
		Key: "proxy", Kind: config.Any, Usage: "Forward the request to a target and reply with its response.",
		Set: func(d *mockDef, v config.Value) error {
			d.proxy = new(proxyDef)
			return withRequired(_proxySchema.Decode(d.proxy, v), d.proxy.hasTarget, v, "target")
		},
	},
	mockField{
		Key: "proxy_all", Kind: config.Any,
		Usage: "Forward what no other mock matches to a target, as proxy does: any request but a CONNECT, and the ones in " +
			"absolute form a forward proxy sends on to their hosts. It comes last, at the highest priority, unless priority " +
			"says otherwise, and needs no request.",
		Set: func(d *mockDef, v config.Value) error {
			d.proxyAll = new(proxyDef)
			return withRequired(_proxySchema.Decode(d.proxyAll, v), d.proxyAll.hasTarget, v, "target")
		},
	},
	mockField{
		Key: "ext", Kind: config.Any, Usage: "Settings for the MockFileHandlers of the server, as an object.",
		Set: func(d *mockDef, v config.Value) error {
			obj, ok := v.Any.(map[string]any)
			if !ok {
				return config.Mismatch(v, "an object")
			}

			d.ext = obj

			return nil
		},
	},
)

var _filePatternSchema = config.NewSchema(
	filePatternField{
		Key: "filename", Kind: config.Any, Usage: "File name to match, as Go reads it: its base. Text or a matcher list.",
		Set: func(d *filePatternDef, v config.Value) error { return withMatcher(v, d.pattern.Filename) },
	},
	filePatternField{
		Key: "content_type", Kind: config.Any,
		Usage: "Media type to match: in lower case, without parameters, or empty when absent. Text or a matcher list.",
		Set:   func(d *filePatternDef, v config.Value) error { return withMatcher(v, d.pattern.ContentType) },
	},
	filePatternField{
		Key: "header", Kind: config.Any, Usage: "Headers of the file's part to match, by name: text or a matcher list each.",
		Set: func(d *filePatternDef, v config.Value) error {
			if err := sameHeader(v); err != nil {
				return err
			}

			return config.Members(v, func(name string, member config.Value) error {
				return withMatcher(member, func(m vlatte.Matcher) *FilePattern { return d.pattern.Header(name, m) })
			})
		},
	},
	filePatternField{
		Key: "body", Kind: config.Any, Usage: "Content to match, as text: text or a matcher list.",
		Set: func(d *filePatternDef, v config.Value) error {
			d.hasBody = true
			return withMatcher(v, func(m vlatte.Matcher) *FilePattern { return d.pattern.Body(m) })
		},
	},
	filePatternField{
		Key: "body_file", Kind: config.String,
		Usage: "File the content must equal. Relative paths start at the mock file's directory.",
		Set: func(d *filePatternDef, v config.Value) error {
			if v.Str == "" {
				return errors.New("expected a file name")
			}

			d.bodyFile = v.Str

			return nil
		},
	},
	filePatternField{
		Key: "every", Kind: config.Bool, Usage: "Match when every file of the field matches, rather than some file.",
		Set: func(d *filePatternDef, v config.Value) error {
			if v.Bool {
				d.pattern.Every()
			}

			return nil
		},
	},
)

var _basicAuthSchema = config.NewSchema(
	basicAuthField{
		Key: "username", Kind: config.Any, Usage: "Username to match: text or a matcher list.",
		Set: func(a *basicAuthDef, v config.Value) error {
			m, err := matcherOf(v)
			a.username = m

			return err
		},
	},
	basicAuthField{
		Key: "password", Kind: config.Any, Usage: "Password to match: text or a matcher list.",
		Set: func(a *basicAuthDef, v config.Value) error {
			m, err := matcherOf(v)
			a.password = m

			return err
		},
	},
)

var _replySchema = config.NewSchema(
	replyField{
		Key: "status", Kind: config.Int, Default: "200", Usage: "Status code.",
		Set: func(r *replyDef, v config.Value) error { r.status = int(v.Int); return checkStatus(v.Int) },
	},
	replyField{
		Key: "header", Kind: config.Any, Usage: "Headers, by name: text or a list of text each.",
		Set: func(r *replyDef, v config.Value) error { return headerValues(v, &r.header) },
	},
	replyField{
		Key: "header_template", Kind: config.Any, Usage: "Headers whose values are templates, by name.",
		Set: func(r *replyDef, v config.Value) error { return headerValues(v, &r.headerTemplate) },
	},
	replyField{
		Key: "trailer", Kind: config.Any, Usage: "Trailers, by name: text or a list of text each.",
		Set: func(r *replyDef, v config.Value) error { return headerValues(v, &r.trailer) },
	},
	replyField{
		Key: "body", Kind: config.Any, Usage: "Body: text as is, anything else as JSON.",
		Set: func(r *replyDef, v config.Value) error {
			if v.Any == nil {
				return config.Invalid(v, v.Key+" is null", `remove it, or write "" for an empty body`)
			}

			r.body, r.hasBody = v.Any, true

			return nil
		},
	},
	replyField{
		Key: "body_file", Kind: config.String, Usage: "File to read the body from. Relative paths start at the mock file's directory.",
		Set: func(r *replyDef, v config.Value) error {
			if v.Str == "" {
				return errors.New("expected a file name")
			}

			r.bodyFile = v.Str

			return nil
		},
	},
	replyField{
		Key: "multipart", Kind: config.Any,
		Usage: "Multipart body: type, boundary, params and parts. It sets the Content-Type, with its boundary.",
		Set: func(r *replyDef, v config.Value) error {
			spec, err := mpart.Decode(v)
			r.multipart = spec

			return err
		},
	},
	replyField{
		Key: "template.enabled", Kind: config.Bool, Usage: "Render the body as a template: a text body, the content of body_file, or the text of multipart parts.",
		Set: func(r *replyDef, v config.Value) error { r.templated = v.Bool; return nil },
	},
	replyField{
		Key: "template.data", Kind: config.Any, Usage: "Data the reply's templates read as .Extra.",
		Set: func(r *replyDef, v config.Value) error {
			switch v.Any.(type) {
			case map[string]any, []any:
				r.data, r.hasData = v.Any, true
				return nil
			default:
				return config.Mismatch(v, "an object or a list")
			}
		},
	},
	replyField{
		Key: "encoding", Kind: config.String, Usage: "Encoding of the body: gzip.",
		Set: func(r *replyDef, v config.Value) error {
			if v.Str != "gzip" {
				return config.Invalid(v, fmt.Sprintf("%s must be gzip, not %q", v.Key, v.Str), "")
			}

			r.gzip = true

			return nil
		},
	},
)

var _sequenceSchema = config.NewSchema(
	sequenceField{
		Key: "responses", Kind: config.Any, Usage: "Replies, served in order.",
		Set: func(s *sequenceDef, v config.Value) error { s.set = true; return decodeReplies(&s.replies, v) },
	},
	sequenceField{
		Key: "after_ended", Kind: config.Any, Usage: "Reply once the responses run out.",
		Set: func(s *sequenceDef, v config.Value) error {
			s.afterEnded = newReplyDef()
			return decodeReply(s.afterEnded, v)
		},
	},
)

var _randomSchema = config.NewSchema(
	randomField{
		Key: "responses", Kind: config.Any, Usage: "Replies, served at random.",
		Set: func(r *randomDef, v config.Value) error { r.set = true; return decodeReplies(&r.replies, v) },
	},
	randomField{
		Key: "seed", Kind: config.Int, Usage: "Seed of the random order, to repeat it.",
		Set: func(r *randomDef, v config.Value) error { r.seed, r.seeded = v.Int, true; return nil },
	},
)

// _proxyFields declare the keys of a proxy reply: those of a mock file's proxy, and of the server's proxy_all.
var _proxyFields = []proxyField{
	{
		Key: "target", Kind: config.String, Usage: "URL of the server to forward requests to. Its path and query come before the request's.",
		Set: func(p *proxyDef, v config.Value) error {
			p.target, p.hasTarget = v.Str, true

			_, err := checkTargetURL(v.Str)

			return err
		},
	},
	{
		Key: "request_header", Kind: config.Any, Usage: "Headers of the forwarded request, by name, in place of the client's. Hop-by-hop ones, such as Connection, fail the mock, except Te: trailers.",
		Set: func(p *proxyDef, v config.Value) error { return headerValues(v, &p.requestHeader) },
	},
	{
		Key: "header", Kind: config.Any, Usage: "Headers of the response, by name, in place of the target's.",
		Set: func(p *proxyDef, v config.Value) error { return headerValues(v, &p.header) },
	},
	{
		Key: "remove_request_headers", Kind: config.List, Usage: "Headers removed from the forwarded request.",
		Set: func(p *proxyDef, v config.Value) error { p.removeRequestHeaders = v.List; return nil },
	},
	{
		Key: "trim_prefix", Kind: config.String, Usage: "Prefix of whole segments removed from the request path, before the target's path is added.",
		Set: func(p *proxyDef, v config.Value) error { p.trimPrefix = v.Str; return nil },
	},
	{
		Key: "trim_suffix", Kind: config.String, Usage: "Text removed from the end of the request path, such as an extension, before the target's path is added.",
		Set: func(p *proxyDef, v config.Value) error { p.trimSuffix = v.Str; return nil },
	},
	{
		Key: "timeout", Kind: config.Duration, Default: _defaultTimeout.String(),
		Usage: "How long to wait for the target's response headers, connecting and sending the request included: " +
			"milliseconds or a Go duration (1.5s). 0 waits forever.",
		Set: func(p *proxyDef, v config.Value) error { p.timeout, p.hasTimeout = v.Dur, true; return nil },
	},
	{
		Key: "ssl_verify", Kind: config.Bool, Usage: "Verify the TLS certificate of the target. Off by default.",
		Set: func(p *proxyDef, v config.Value) error { p.sslVerify, p.hasSSLVerify = v.Bool, true; return nil },
	},
	{
		Key: "x_forwarded", Kind: config.Bool,
		Usage: "Set X-Forwarded-For, X-Forwarded-Host and X-Forwarded-Proto on the forwarded request, with the client's address, host and scheme.",
		Set:   func(p *proxyDef, v config.Value) error { p.xForwarded = v.Bool; return nil },
	},
	{
		Key: "preserve_host", Kind: config.Bool, Usage: "Send the target the Host the client asked for, in place of the target's.",
		Set: func(p *proxyDef, v config.Value) error { p.preserveHost = v.Bool; return nil },
	},
}

var _proxySchema = config.NewSchema(_proxyFields...)

var _eventSchema = config.NewSchema(
	eventField{
		Key: "name", Kind: config.String, Usage: "Name of the registered listener to run.",
		Set: func(e *vlatte.EventConfig, v config.Value) error { e.Name = v.Str; return nil },
	},
	eventField{
		Key: "parameters", Kind: config.Any, Usage: "Parameters of the listener.",
		Set: func(e *vlatte.EventConfig, v config.Value) error { e.Parameters = v.Any; return nil },
	},
)

func newMockDef() mockDef {
	return mockDef{enabled: true}
}

func newReplyDef() *replyDef {
	return &replyDef{status: 200}
}

// check reports the problems between keys of a decoded document that came from origin. found are the problems
// decoding already found: a request key that failed to decode still counts as a matcher.
func (d *mockDef) check(origin string, found []config.Problem) []config.Problem {
	var set []string
	for i, present := range []bool{d.reply != nil, d.sequence != nil, d.random != nil, d.proxy != nil, d.proxyAll != nil} {
		if present {
			set = append(set, _responseKeys[i])
		}
	}

	problems := conflicts(origin, "", set)

	var urlKeys []string
	for _, k := range _urlKeys {
		if _, ok := d.request.url[k]; ok {
			urlKeys = append(urlKeys, k)
		}
	}

	problems = append(problems, conflicts(origin, "request", urlKeys)...)
	problems = append(problems, d.request.checkPathParams(origin, found)...)

	requestFailed := slices.ContainsFunc(found, func(p config.Problem) bool {
		return p.Key == "request" || strings.HasPrefix(p.Key, "request.")
	})

	// proxy_all matches any request on its own.
	if len(d.request.keys) == 0 && !requestFailed && d.proxyAll == nil {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: origin, Key: "request",
			Message: "request sets no matcher", Hint: `to match every request, write request: {path: "*"}`,
		})
	}

	if d.scenario.set && d.scenario.name == "" {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: origin, Key: "scenario.name",
			Message: "scenario is set but scenario.name is not", Hint: "name the scenario",
		})
	}

	return problems
}

// checkPathParams reports path_params without a path_template, and the names under it that the template doesn't have.
// A path_params that isn't an object, and a path_template that failed to parse, have their own problems in found.
func (r *requestDef) checkPathParams(origin string, found []config.Problem) []config.Problem {
	if !slices.Contains(r.keys, "path_params") ||
		slices.ContainsFunc(found, func(p config.Problem) bool { return p.Key == "request.path_params" }) {
		return nil
	}

	t := r.pathTemplate
	if t == nil {
		if slices.Contains(r.keys, "path_template") {
			return nil
		}

		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: origin, Key: "request.path_params",
			Message: "request.path_params is set without request.path_template",
			Hint:    "name the parameters in request.path_template, such as /users/{id}",
		}}
	}

	hint := "request.path_template has no parameters"
	if names := t.paramNames(); len(names) > 0 {
		hint = "its parameters are " + strings.Join(names, ", ")
	}

	var problems []config.Problem
	for _, name := range r.pathParamNames {
		if !t.has(name) {
			key := "request.path_params." + name
			problems = append(problems, config.Problem{
				Code: config.CodeUnknownKey, Origin: origin, Key: key,
				Message: fmt.Sprintf("%s names no parameter of request.path_template %q", key, t.text), Hint: hint,
			})
		}
	}

	return problems
}

// conflicts reports each key of set after the first as set with the first: only one of them can be set.
func conflicts(origin, prefix string, set []string) []config.Problem {
	if len(set) < 2 {
		return nil
	}

	first := under(prefix, set[0])

	problems := make([]config.Problem, 0, len(set)-1)
	for _, k := range set[1:] {
		problems = append(problems, config.Problem{
			Code: config.CodeConflict, Origin: origin, Key: under(prefix, k),
			Message: fmt.Sprintf("%s is set with %s", under(prefix, k), first), Hint: "keep one of them",
		})
	}

	return problems
}

func under(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}

// withRequired adds a missing_value problem for key, under v.Key, to the result of decoding v when present is false.
// A v that isn't an object, or a key whose value failed to decode, already has its problem.
func withRequired(err error, present bool, v config.Value, key string) error {
	if _, isObject := v.Any.(map[string]any); present || !isObject {
		return err
	}

	var problems []config.Problem
	if cfgErr, ok := errors.AsType[*config.Error](err); ok {
		problems = cfgErr.Problems
	}

	full := v.Key + "." + key
	if slices.ContainsFunc(problems, func(p config.Problem) bool { return p.Key == full }) {
		return err
	}

	return &config.Error{Problems: append(problems, config.Problem{
		Code: config.CodeMissingValue, Origin: v.Origin, Key: full,
		Message: full + " is required",
	})}
}

// decodeReply decodes a reply object into r and checks it.
func decodeReply(r *replyDef, v config.Value) error {
	err := _replySchema.Decode(r, v)

	obj, _ := v.Any.(map[string]any)

	var bodies []string
	for _, key := range []string{"body", "body_file", "multipart"} {
		if _, ok := obj[key]; ok {
			bodies = append(bodies, key)
		}
	}

	problems := conflicts(v.Origin, v.Key, bodies)

	// A template is text: a JSON body would be written as it is, with nothing rendered.
	if _, isText := r.body.(string); r.templated && r.hasBody && !isText {
		key := v.Key + ".template.enabled"
		problems = append(problems, config.Problem{
			Code: config.CodeConflict, Origin: v.Origin, Key: key,
			Message: key + " is set with " + v.Key + ".body, which isn't text",
			Hint:    "write the body as a string to render it as a template",
		})
	}

	// A multipart body sets the Content-Type, with its boundary.
	if _, ok := obj["multipart"]; ok {
		for _, headers := range []string{"header", "header_template"} {
			names, _ := obj[headers].(map[string]any)
			for _, name := range slices.Sorted(maps.Keys(names)) {
				if http.CanonicalHeaderKey(name) == HeaderContentType {
					key := v.Key + "." + headers + "." + name
					problems = append(problems, config.Problem{
						Code: config.CodeConflict, Origin: v.Origin, Key: key,
						Message: key + " is set with " + v.Key + ".multipart",
						Hint:    "a multipart body sets the Content-Type: remove the header",
					})
				}
			}
		}
	}

	if len(problems) == 0 {
		return err
	}

	if cfgErr, ok := errors.AsType[*config.Error](err); ok {
		problems = append(cfgErr.Problems, problems...)
	}

	return &config.Error{Problems: problems}
}

// decodeReplies decodes a non-empty list of reply objects.
func decodeReplies(replies *[]*replyDef, v config.Value) error {
	if list, ok := v.Any.([]any); ok && len(list) == 0 {
		return config.Invalid(v, v.Key+" is empty", "add at least one reply")
	}

	return config.Items(v, func(item config.Value) error {
		r := newReplyDef()
		if err := decodeReply(r, item); err != nil {
			return err
		}

		*replies = append(*replies, r)

		return nil
	})
}

// decodeFormFiles decodes request.form_file: by field name, a pattern, or a list of patterns.
func decodeFormFiles(d *mockDef, v config.Value) error {
	return config.Members(v, func(field string, member config.Value) error {
		if _, isList := member.Any.([]any); isList {
			return config.Items(member, func(item config.Value) error { return decodeFilePattern(d, field, item) })
		}

		return decodeFilePattern(d, field, member)
	})
}

// decodeFilePattern decodes a pattern of the files of field, and reads its body_file.
func decodeFilePattern(d *mockDef, field string, v config.Value) error {
	def := filePatternDef{pattern: FileWith()}

	err := _filePatternSchema.Decode(&def, v)
	if def.hasBody && def.bodyFile != "" {
		return &config.Error{Problems: append(config.ProblemsOf(v, err), conflicts(v.Origin, v.Key, []string{"body", "body_file"})...)}
	}

	if err != nil {
		return err
	}

	if def.bodyFile != "" {
		// Joined to the mock file's directory, the name opens the same file from the working directory, as the
		// pattern's description says it.
		path := bodyfile.Path(d.dir, def.bodyFile)

		b, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return &config.Error{Problems: []config.Problem{{
				Code: config.CodeFileNotFound, Origin: v.Origin, Key: v.Key + ".body_file",
				Message: fmt.Sprintf("%s.body_file %q does not exist", v.Key, path),
				Hint:    "relative paths start at the mock file's directory",
			}}}
		case err != nil:
			return &config.Error{Problems: []config.Problem{{
				Code: config.CodeReadError, Origin: v.Origin, Key: v.Key + ".body_file", Message: err.Error(),
			}}}
		}

		def.pattern.bodyFileBytes(path, b)
	}

	d.request.formFile = append(d.request.formFile, func(b *Builder) { b.FormFile(field, def.pattern) })

	return nil
}

// withMatcher calls set with the matcher of a value: text, compared as is, or a matcher list.
func withMatcher(v config.Value, set func(vlatte.Matcher) *FilePattern) error {
	m, err := matcherOf(v)
	if err != nil {
		return err
	}

	set(m)

	return nil
}

// setEvents decodes a list of on_complete events, each naming its listener.
func setEvents(events *[]vlatte.EventConfig, dir string, v config.Value) error {
	return config.Items(v, func(item config.Value) error {
		e := vlatte.EventConfig{Dir: dir}
		if err := _eventSchema.Decode(&e, item); err != nil {
			return err
		}

		if e.Name == "" {
			return &config.Error{Problems: []config.Problem{{
				Code: config.CodeMissingValue, Origin: item.Origin, Key: item.Key + ".name",
				Message: item.Key + ".name is required", Hint: "name a listener registered with Setup().OnCompleteListener",
			}}}
		}

		*events = append(*events, e)

		return nil
	})
}

// headerValues decodes an object of header values: a string, or a list of strings, each.
func headerValues(v config.Value, dst *[]headerValue) error {
	return config.Members(v, func(name string, m config.Value) error {
		switch x := m.Any.(type) {
		case string:
			*dst = append(*dst, headerValue{name: name, value: x})
		case []any:
			values := make([]headerValue, 0, len(x))

			err := config.Items(m, func(item config.Value) error {
				s, ok := item.Any.(string)
				if !ok {
					return config.Mismatch(item, "a string", config.String)
				}

				values = append(values, headerValue{name: name, value: s})

				return nil
			})
			if err != nil {
				return err
			}

			*dst = append(*dst, values...)
		default:
			return config.Mismatch(m, "a string or a list of strings", config.String)
		}

		return nil
	})
}

// sameHeader reports header names that differ only in case: they name one header, so a request can't match
// different matchers for them.
func sameHeader(v config.Value) error {
	obj, ok := v.Any.(map[string]any)
	if !ok {
		return nil
	}

	var problems []config.Problem

	seen := make(map[string]string, len(obj))
	for _, name := range slices.Sorted(maps.Keys(obj)) {
		canonical := http.CanonicalHeaderKey(name)
		if first, ok := seen[canonical]; ok {
			problems = append(problems, config.Problem{
				Code: config.CodeConflict, Origin: v.Origin, Key: v.Key + "." + name,
				Message: fmt.Sprintf("%s.%s and %s.%s name the same header", v.Key, name, v.Key, first),
				Hint:    "header names ignore case: keep one of them",
			})

			continue
		}

		seen[canonical] = name
	}

	if len(problems) > 0 {
		return &config.Error{Problems: problems}
	}

	return nil
}

// matcherOf builds the matcher of a value: a string, compared as is, or a matcher list such as [contains, x].
func matcherOf(v config.Value) (vlatte.Matcher, error) {
	switch x := v.Any.(type) {
	case string:
		return filterbuilder.TryBuildMatcher(x)
	case []any:
		if len(x) == 0 {
			return nil, config.Invalid(v, v.Key+" is an empty list", "name a matcher, such as [equal, x]")
		}

		return filterbuilder.TryBuildMatcher(x)
	default:
		return nil, config.Mismatch(v, "a string or a matcher list, such as [equal, x]", config.String)
	}
}

// matchText calls text with a text value, or matcher with the matcher of a matcher list.
func matchText(v config.Value, text func(string), matcher func(vlatte.Matcher)) error {
	if s, ok := v.Any.(string); ok {
		text(s)
		return nil
	}

	m, err := matcherOf(v)
	if err != nil {
		return err
	}

	matcher(m)

	return nil
}

// matchEach decodes an object of matcher values, by name, into Builder calls in name order.
func matchEach(v config.Value, dst *[]func(*Builder), add func(b *Builder, name string, m vlatte.Matcher)) error {
	return config.Members(v, func(name string, member config.Value) error {
		m, err := matcherOf(member)
		if err != nil {
			return err
		}

		*dst = append(*dst, func(b *Builder) { add(b, name, m) })

		return nil
	})
}

// matchLists is matchEach for headers and forms, whose matchers get every value, as a list: text, which never equals a
// list, is a problem.
func matchLists(v config.Value, dst *[]func(*Builder), add func(b *Builder, name string, m vlatte.Matcher)) error {
	return config.Members(v, func(name string, member config.Value) error {
		if s, isText := member.Any.(string); isText {
			return config.Invalid(member, fmt.Sprintf("%s=%q is text, which never equals a list of values", member.Key, s),
				fmt.Sprintf("write a matcher list, such as [contains, %q]", s))
		}

		m, err := matcherOf(member)
		if err != nil {
			return err
		}

		*dst = append(*dst, func(b *Builder) { add(b, name, m) })

		return nil
	})
}

func setMethod(r *requestDef, v config.Value) error {
	s, ok := v.Any.(string)
	if !ok {
		m, err := matcherOf(v)
		if err != nil {
			return err
		}

		r.method = func(b *Builder) { b.MethodMatches(m) }

		return nil
	}

	methods := strings.Split(s, ",")
	for i, method := range methods {
		methods[i] = strings.TrimSpace(method)
		if methods[i] == "" {
			return config.Invalid(v, fmt.Sprintf("%s=%q holds an empty method", v.Key, s), "separate methods with commas, such as GET, POST")
		}
	}

	r.method = func(b *Builder) { b.Method(methods...) }

	return nil
}

// setHost sets the host matcher. Text is a host name, which ignores case.
func setHost(r *requestDef, v config.Value) error {
	s, ok := v.Any.(string)
	if !ok {
		m, err := matcherOf(v)
		if err != nil {
			return err
		}

		r.host = func(b *Builder) { b.Host(m) }

		return nil
	}

	if err := bareHost(v, s); err != nil {
		return err
	}

	r.host = func(b *Builder) { b.Host(match.EqualIgnoreCase(s)) }

	return nil
}

// bareHost reports text of host or client_ip with a port, or the brackets of an IPv6 address: the value it compares
// with has neither, so it would never match.
func bareHost(v config.Value, s string) error {
	if host := hostOnly(s); host != s {
		return config.Invalid(v, fmt.Sprintf("%s=%q has a port or brackets, which the value it matches never has", v.Key, s),
			fmt.Sprintf("write the address alone: %q", host))
	}

	return nil
}

func setURL(r *requestDef, v config.Value) error {
	r.keys = append(r.keys, "url")
	r.url = withURL(r.url)

	s, ok := v.Any.(string)
	if !ok {
		m, err := matcherOf(v)
		if err != nil {
			return err
		}

		r.url["url"] = func(b *Builder) { b.URL(m) }

		return nil
	}

	// A relative URL names the path and query a request must have: the path starts with "/", and reads as a server
	// reads a request target. It is exact, so a "*" in its path, which was a glob, would match only itself.
	if strings.HasPrefix(s, "/") {
		target, _, _ := strings.Cut(s, "#")
		if path, _, _ := strings.Cut(target, "?"); strings.Contains(path, "*") {
			return config.Invalid(v, fmt.Sprintf("%s=%q has a * in its path, which matches exactly", v.Key, s),
				"to match the path with a glob, use path; for a literal *, write %2A")
		}

		m, err := filterbuilder.BuildMatcher([]any{"requesturi", s})
		if err != nil {
			return config.Invalid(v, fmt.Sprintf("%s=%q is not a URL: %v", v.Key, s, err), "")
		}

		r.url["url"] = func(b *Builder) { b.URL(m) }

		return nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return config.Invalid(v, fmt.Sprintf("%s=%q is not a URL: %v", v.Key, s, err), "")
	}

	if !u.IsAbs() {
		return config.Invalid(v, fmt.Sprintf("%s=%q must be an absolute URL, or start with /", v.Key, s),
			"to match the path with a glob, use path instead")
	}

	r.url["url"] = func(b *Builder) { b.URL(match.EqualIgnoreCase(s)) }

	return nil
}

func setPathTemplate(r *requestDef, v config.Value) error {
	r.keys = append(r.keys, "path_template")

	t, err := parsePathTemplate(v.Str)
	if err != nil {
		return config.Invalid(v, fmt.Sprintf("%s=%q is not a path template: %v", v.Key, v.Str, err),
			"a parameter is a whole segment, such as /users/{id}")
	}

	r.pathTemplate = t

	return nil
}

func setURLMatch(r *requestDef, v config.Value) error {
	r.keys = append(r.keys, "url_match")
	r.url = withURL(r.url)

	s := v.Str
	if _, err := regexp.Compile(s); err != nil {
		return config.Invalid(v, fmt.Sprintf("%s must be a regular expression, not %q", v.Key, s), err.Error())
	}

	r.url["url_match"] = func(b *Builder) { b.URL(match.RegExpr(s)) }

	return nil
}

func withURL(m map[string]func(*Builder)) map[string]func(*Builder) {
	if m == nil {
		return make(map[string]func(*Builder), 1)
	}

	return m
}

// apply sets the mock on b. dir is where relative body files open from.
func (d *mockDef) apply(b *Builder, dir string) {
	if d.id != "" {
		b.ID(d.id)
	}

	b.Name(d.name)
	b.Priority(d.priority)
	b.Enabled(d.enabled)

	if d.repeat > 0 {
		b.Times(d.repeat)
	}

	if d.scenario.set {
		b.ScenarioIs(d.scenario.name).ScenarioStateIs(d.scenario.requiredState).ScenarioStateWillBe(d.scenario.newState)
	}

	if d.hasDelay {
		b.Delay(d.delay)
	}

	for _, e := range d.events {
		b.OnCompleteEvent(e)
	}

	d.request.apply(b)

	if d.proxyAll != nil {
		proxyAll(b, d.proxyAll.build())

		// A proxy_all comes last, unless the file says otherwise.
		if d.hasPriority {
			b.Priority(d.priority)
		}

		return
	}

	b.Reply(d.replier(dir))
}

// apply adds the request matchers to b, in a fixed order.
func (r *requestDef) apply(b *Builder) {
	for _, set := range []func(*Builder){r.scheme, r.method, r.host, r.clientIP, r.basicAuth} {
		if set != nil {
			set(b)
		}
	}

	for _, k := range _urlKeys {
		if set, ok := r.url[k]; ok {
			set(b)
		}
	}

	if r.pathTemplate != nil {
		b.URLPathTemplate(r.pathTemplate.text)
	}

	if r.fragment != nil {
		r.fragment(b)
	}

	for _, sets := range [][]func(*Builder){r.query, r.queries, r.header, r.headers, r.cookie, r.form, r.forms, r.formFile, r.pathParams} {
		for _, set := range sets {
			set(b)
		}
	}

	if r.body != nil {
		r.body(b)
	}
}

func (d *mockDef) replier(dir string) ReplierFactory {
	switch {
	case d.reply != nil:
		return d.reply.build(dir)
	case d.sequence != nil:
		seq := Seq()
		for _, r := range d.sequence.replies {
			seq.Add(r.build(dir))
		}

		if d.sequence.afterEnded != nil {
			seq.OnSequenceEnded(d.sequence.afterEnded.build(dir))
		}

		return seq
	case d.random != nil:
		random := Rand()
		if d.random.seeded {
			random = RandWithSeed(d.random.seed)
		}

		for _, r := range d.random.replies {
			random.Add(r.build(dir))
		}

		return random
	case d.proxy != nil:
		return d.proxy.build()
	default:
		return OK()
	}
}

func (r *replyDef) build(dir string) *Reply {
	rep := NewReply().Status(r.status)

	for _, h := range r.header {
		rep.Header(h.name, h.value)
	}

	for _, h := range r.headerTemplate {
		rep.HeaderTemplate(h.name, h.value)
	}

	for _, h := range r.trailer {
		rep.Trailer(h.name, h.value)
	}

	if r.gzip {
		rep.Gzip()
	}

	templated := r.templated
	if r.hasData {
		rep.SetTemplateData(r.data)
	}

	switch {
	case r.multipart != nil:
		spec := *r.multipart
		spec.Templated = templated
		rep.bodyFileDir = dir
		rep.Multipart(&Multipart{spec: spec})
	case r.bodyFile != "":
		rep.bodyFileDir = dir
		if templated {
			rep.BodyFileWithTemplate(r.bodyFile)
		} else {
			rep.BodyFile(r.bodyFile)
		}
	case r.hasBody:
		s, isText := r.body.(string)
		switch {
		case isText && templated:
			rep.BodyTemplate(s)
		case isText:
			rep.Body([]byte(s))
		default:
			rep.BodyJSON(r.body)
		}
	}

	return rep
}

func (p *proxyDef) build() *ProxyReply {
	proxy := From(p.target)

	for _, h := range p.requestHeader {
		proxy.RequestHeader(h.name, h.value)
	}

	for _, h := range p.header {
		proxy.Header(h.name, h.value)
	}

	if len(p.removeRequestHeaders) > 0 {
		proxy.RemoveRequestHeaders(p.removeRequestHeaders...)
	}

	if p.trimPrefix != "" {
		proxy.TrimPrefix(p.trimPrefix)
	}

	if p.trimSuffix != "" {
		proxy.TrimSuffix(p.trimSuffix)
	}

	if p.hasTimeout {
		proxy.Timeout(p.timeout)
	}

	if p.hasSSLVerify {
		proxy.SSLVerify(p.sslVerify)
	}

	if p.xForwarded {
		proxy.XForwarded()
	}

	if p.preserveHost {
		proxy.PreserveHost()
	}

	return proxy
}
