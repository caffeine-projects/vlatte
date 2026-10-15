package mockhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
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
	replyField       = config.Field[replyDef]
	sequenceField    = config.Field[sequenceDef]
	randomField      = config.Field[randomDef]
	proxyField       = config.Field[proxyDef]
	eventField       = config.Field[vlatte.EventConfig]
)

// mockDef is a decoded mock file: what its keys set, before it is applied to a Builder.
type mockDef struct {
	id       string
	name     string
	priority int
	enabled  bool
	delay    time.Duration
	hasDelay bool
	repeat   int64
	scenario scenarioDef
	events   []vlatte.EventConfig
	request  requestDef
	reply    *replyDef
	sequence *sequenceDef
	random   *randomDef
	proxy    *proxyDef
	ext      map[string]any
	dir      string // where relative files open from: the mock file's directory; "" is the working directory
}

type scenarioDef struct {
	set           bool
	name          string
	requiredState string
	newState      string
}

// requestDef holds the request matchers, as Builder calls, and the keys that set them.
type requestDef struct {
	keys     []string
	scheme   func(*Builder)
	method   func(*Builder)
	url      map[string]func(*Builder) // by key: url, url_match or path; only one may be set
	fragment func(*Builder)
	query    []func(*Builder)
	queries  []func(*Builder)
	header   []func(*Builder)
	form     []func(*Builder)
	formFile []func(*Builder)
	body     func(*Builder)
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
	target        string
	forwardHeader []headerValue
	header        []headerValue
	removeHeaders []string
	trimPrefix    string
	trimSuffix    string
	timeout       time.Duration
	hasTimeout    bool
	sslVerify     bool
	hasSSLVerify  bool
	hasTarget     bool
}

// _responseKeys are the ways to reply, in the order conflicts are reported.
var _responseKeys = []string{"response", "response_sequence", "response_random", "proxy"}

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
		Set: func(d *mockDef, v config.Value) error { d.priority = int(v.Int); return nil },
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
		Key: "request.form", Kind: config.Any, Usage: "Fields of a urlencoded or multipart form body to match, by name: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "form")
			return matchEach(v, &d.request.form, func(b *Builder, name string, m vlatte.Matcher) { b.FormField(name, m) })
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

var _proxySchema = config.NewSchema(
	proxyField{
		Key: "target", Kind: config.String, Usage: "URL of the server to forward requests to.",
		Set: func(p *proxyDef, v config.Value) error {
			p.target, p.hasTarget = v.Str, true

			u, err := url.Parse(v.Str)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return errors.New("expected an absolute URL, such as http://api.local:8080")
			}

			return nil
		},
	},
	proxyField{
		Key: "forward_header", Kind: config.Any, Usage: "Headers added to the forwarded request, by name.",
		Set: func(p *proxyDef, v config.Value) error { return headerValues(v, &p.forwardHeader) },
	},
	proxyField{
		Key: "header", Kind: config.Any, Usage: "Headers added to the response, by name.",
		Set: func(p *proxyDef, v config.Value) error { return headerValues(v, &p.header) },
	},
	proxyField{
		Key: "remove_headers", Kind: config.List, Usage: "Headers removed from the forwarded request.",
		Set: func(p *proxyDef, v config.Value) error { p.removeHeaders = v.List; return nil },
	},
	proxyField{
		Key: "trim_prefix", Kind: config.String, Usage: "Prefix removed from the forwarded path.",
		Set: func(p *proxyDef, v config.Value) error { p.trimPrefix = v.Str; return nil },
	},
	proxyField{
		Key: "trim_suffix", Kind: config.String, Usage: "Suffix removed from the forwarded path.",
		Set: func(p *proxyDef, v config.Value) error { p.trimSuffix = v.Str; return nil },
	},
	proxyField{
		Key: "timeout", Kind: config.Duration,
		Usage: "How long to wait for the target's response headers, connecting and sending the request included: " +
			"milliseconds or a Go duration (1.5s). 0 waits forever.",
		Set: func(p *proxyDef, v config.Value) error { p.timeout, p.hasTimeout = v.Dur, true; return nil },
	},
	proxyField{
		Key: "ssl_verify", Kind: config.Bool, Usage: "Verify the TLS certificate of the target.",
		Set: func(p *proxyDef, v config.Value) error { p.sslVerify, p.hasSSLVerify = v.Bool, true; return nil },
	},
)

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
	for i, present := range []bool{d.reply != nil, d.sequence != nil, d.random != nil, d.proxy != nil} {
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

	requestFailed := slices.ContainsFunc(found, func(p config.Problem) bool {
		return p.Key == "request" || strings.HasPrefix(p.Key, "request.")
	})

	if len(d.request.keys) == 0 && !requestFailed {
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
		path := def.bodyFile
		if d.dir != "" && !filepath.IsAbs(path) {
			path = filepath.Join(d.dir, path)
		}

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

		def.pattern.bodyFileBytes(def.bodyFile, b)
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

	b.Reply(d.replier(dir))
}

// apply adds the request matchers to b, in a fixed order.
func (r *requestDef) apply(b *Builder) {
	for _, set := range []func(*Builder){r.scheme, r.method} {
		if set != nil {
			set(b)
		}
	}

	for _, k := range _urlKeys {
		if set, ok := r.url[k]; ok {
			set(b)
		}
	}

	if r.fragment != nil {
		r.fragment(b)
	}

	for _, sets := range [][]func(*Builder){r.query, r.queries, r.header, r.form, r.formFile} {
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

	for _, h := range p.forwardHeader {
		proxy.ForwardHeader(h.name, h.value)
	}

	for _, h := range p.header {
		proxy.Header(h.name, h.value)
	}

	if len(p.removeHeaders) > 0 {
		proxy.RemoveProxyHeaders(p.removeHeaders...)
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

	return proxy
}
