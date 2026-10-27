package mockgrpc

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mockid"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

// The gRPC mock file format is declared here, one table per object of a mock file. The tables decode a document
// into the *Def types below; mockDoc.build checks them, then applies them to a Builder.

type (
	mockField   = config.Field[mockDef]
	replyField  = config.Field[replyDef]
	statusField = config.Field[statusDef]
	bidiField   = config.Field[bidiDef]
	proxyField  = config.Field[proxyDef]
	ruleField   = config.Field[ruleDef]
	eventField  = config.Field[vlatte.EventConfig]
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
	bidi        *bidiDef
	proxy       *proxyDef
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
	keys    []string
	method  string
	header  []func(*Builder)
	fields  []func(*Builder)
	json    *string // message or messages, as JSON text
	body    vlatte.Matcher
	message []string // the keys of json that were set: message, messages
}

// replyDef is a decoded response: of a mock, or of a rule of a bidi one.
type replyDef struct {
	dir       string   // where body_file opens from
	keys      []string // the keys that set messages: message, messages, body_file
	header    []headerValue
	trailer   []headerValue
	messages  []string // JSON texts
	templated bool
	status    *statusDef
	delay     time.Duration
	hasDelay  bool
}

type statusDef struct {
	code    codes.Code
	message string
	details []string // JSON texts of Any messages
}

type bidiDef struct {
	dir       string // where the body_file of its responses open from
	header    []headerValue
	trailer   []headerValue
	rules     []ruleDef
	otherwise *replyDef
	onEnd     *replyDef
}

type ruleDef struct {
	dir      string
	when     vlatte.Matcher
	response *replyDef
}

type proxyDef struct {
	target string
	header []headerValue
	remove []string
}

type headerValue struct {
	name  string
	value string
}

var _mockSchema = config.NewSchema(
	mockField{
		Key: "id", Kind: config.String, Usage: "ID of the mock, in place of a generated one: letters, digits, '.', '_', '~' and '-'.",
		Set: func(d *mockDef, v config.Value) error { d.id = v.Str; return mockid.Check(v.Str) },
	},
	mockField{
		Key: "name", Kind: config.String, Usage: "Name of the mock, shown in logs and debug headers.",
		Set: func(d *mockDef, v config.Value) error { d.name = v.Str; return nil },
	},
	mockField{
		Key: "priority", Kind: config.Int, Usage: "Mocks with a lower priority are matched first.",
		Set: func(d *mockDef, v config.Value) error { d.priority, d.hasPriority = int(v.Int), true; return nil },
	},
	mockField{
		Key: "enabled", Kind: config.Bool, Default: "true", Usage: "Whether the mock can match calls.",
		Set: func(d *mockDef, v config.Value) error { d.enabled = v.Bool; return nil },
	},
	mockField{
		Key: "delay", Kind: config.Duration, Usage: "How long to wait before replying: milliseconds or a Go duration (1.5s).",
		Set: func(d *mockDef, v config.Value) error { d.delay, d.hasDelay = v.Dur, true; return nil },
	},
	mockField{
		Key: "repeat", Kind: config.Int, Usage: "How many calls the mock answers.",
		Set: func(d *mockDef, v config.Value) error {
			d.repeat = v.Int
			if v.Int < 1 {
				return errors.New("expected 1 or more")
			}

			return nil
		},
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
		Key: "scenario.new_state", Kind: config.String, Usage: "State the scenario moves to once the mock answered.",
		Set: func(d *mockDef, v config.Value) error { d.scenario.set, d.scenario.newState = true, v.Str; return nil },
	},
	mockField{
		Key: "on_complete", Kind: config.Any, Usage: "Events to run once the mock replied: a list of {name, parameters}.",
		Set: func(d *mockDef, v config.Value) error { return setEvents(&d.events, d.dir, v) },
	},
	mockField{
		Key: "request.method", Kind: config.String,
		Usage: "Method to mock, by whole segments of its full method: Method, Service/Method, pkg.Service/Method or /pkg.Service/Method.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "method")
			d.request.method = v.Str

			return nil
		},
	},
	mockField{
		Key: "request.header", Kind: config.Any, Usage: "Metadata to match, by key: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "header")
			return matchEach(v, &d.request.header, func(b *Builder, key string, m vlatte.Matcher) { b.Header(key, m) })
		},
	},
	mockField{
		Key: "request.fields", Kind: config.Any,
		Usage: "Fields of the request to match, by JSON path, such as user_id or items[0].key: text or a matcher list each.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "fields")
			return matchEach(v, &d.request.fields, func(b *Builder, path string, m vlatte.Matcher) { b.Field(path, m) })
		},
	},
	mockField{
		Key: "request.message", Kind: config.Any, Usage: "Request message to match whole, as protojson reads it: an object, or JSON text.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "message")
			d.request.message = append(d.request.message, "message")

			text, err := jsonText(v)
			d.request.json = &text

			return err
		},
	},
	mockField{
		Key: "request.messages", Kind: config.Any, Usage: "Messages of a client stream to match whole, in order: a list of objects.",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "messages")
			d.request.message = append(d.request.message, "messages")

			if _, ok := v.Any.([]any); !ok {
				return config.Mismatch(v, "a list of messages")
			}

			text, err := jsonText(v)
			d.request.json = &text

			return err
		},
	},
	mockField{
		Key: "request.body", Kind: config.Any, Usage: "Matcher list of the request as JSON text, such as [contains, ann].",
		Set: func(d *mockDef, v config.Value) error {
			d.request.keys = append(d.request.keys, "body")

			m, err := matcherOf(v)
			d.request.body = m

			return err
		},
	},
	mockField{
		Key: "response", Kind: config.Any, Usage: "The answer: messages, header, trailer, status and message_delay.",
		Set: func(d *mockDef, v config.Value) error {
			d.reply = &replyDef{dir: d.dir}
			return _replySchema.Decode(d.reply, v)
		},
	},
	mockField{
		Key: "bidi", Kind: config.Any, Usage: "The answer of a bidi streaming method, message by message: header, trailer, on, otherwise and on_end.",
		Set: func(d *mockDef, v config.Value) error {
			d.bidi = &bidiDef{dir: d.dir}
			return _bidiSchema.Decode(d.bidi, v)
		},
	},
	mockField{
		Key: "proxy", Kind: config.Any, Usage: "The answer of another gRPC server, the target, to which the call goes on: target, header and remove_headers.",
		Set: func(d *mockDef, v config.Value) error {
			d.proxy = &proxyDef{}
			return _proxySchema.Decode(d.proxy, v)
		},
	},
)

var _replySchema = config.NewSchema(
	replyField{
		Key: "message", Kind: config.Any, Usage: "The message to answer, as protojson reads it: an object, or JSON text, a template's included.",
		Set: func(r *replyDef, v config.Value) error {
			r.keys = append(r.keys, "message")

			text, err := jsonText(v)
			r.messages = []string{text}

			return err
		},
	},
	replyField{
		Key: "messages", Kind: config.Any, Usage: "The messages of a stream, in order: a list of objects, or of JSON texts.",
		Set: func(r *replyDef, v config.Value) error {
			r.keys = append(r.keys, "messages")
			r.messages = []string{}

			return config.Items(v, func(item config.Value) error {
				text, err := jsonText(item)
				r.messages = append(r.messages, text)

				return err
			})
		},
	},
	replyField{
		Key: "body_file", Kind: config.String, Usage: "JSON file of the answer, relative to the mock file: a message, or a list of messages.",
		Set: func(r *replyDef, v config.Value) error {
			r.keys = append(r.keys, "body_file")
			return readMessages(r, v)
		},
	},
	replyField{
		Key: "template", Kind: config.Bool, Usage: "Render the messages as templates, for each call: write them as JSON text.",
		Set: func(r *replyDef, v config.Value) error { r.templated = v.Bool; return nil },
	},
	replyField{
		Key: "header", Kind: config.Any, Usage: "Metadata the answer starts with, by key: a string or a list of strings each.",
		Set: func(r *replyDef, v config.Value) error { return headerValues(v, &r.header) },
	},
	replyField{
		Key: "trailer", Kind: config.Any, Usage: "Metadata the answer ends with, by key: a string or a list of strings each.",
		Set: func(r *replyDef, v config.Value) error { return headerValues(v, &r.trailer) },
	},
	replyField{
		Key: "status", Kind: config.Any, Usage: "The status the answer ends with, after its messages: code, message and details.",
		Set: func(r *replyDef, v config.Value) error {
			r.status = &statusDef{}
			return _statusSchema.Decode(r.status, v)
		},
	},
	replyField{
		Key: "message_delay", Kind: config.Duration, Usage: "How long to wait before each message: milliseconds or a Go duration (1.5s).",
		Set: func(r *replyDef, v config.Value) error { r.delay, r.hasDelay = v.Dur, true; return nil },
	},
)

var _statusSchema = config.NewSchema(
	statusField{
		Key: "code", Kind: config.Any, Usage: "Code of the status: its name, such as NOT_FOUND, or its number.",
		Set: func(s *statusDef, v config.Value) error {
			c, err := codeOf(v)
			s.code = c

			return err
		},
	},
	statusField{
		Key: "message", Kind: config.String, Usage: "Message of the status.",
		Set: func(s *statusDef, v config.Value) error { s.message = v.Str; return nil },
	},
	statusField{
		Key: "details", Kind: config.Any,
		Usage: `Details of the status, such as google.rpc error details: a list of objects with their "@type".`,
		Set: func(s *statusDef, v config.Value) error {
			return config.Items(v, func(item config.Value) error {
				obj, ok := item.Any.(map[string]any)
				if !ok {
					return config.Mismatch(item, "an object with its @type")
				}

				if _, ok := obj["@type"]; !ok {
					return config.Invalid(item, item.Key+" has no @type", `name its type, such as "type.googleapis.com/google.rpc.ErrorInfo"`)
				}

				text, err := jsonText(item)
				s.details = append(s.details, text)

				return err
			})
		},
	},
)

var _bidiSchema = config.NewSchema(
	bidiField{
		Key: "header", Kind: config.Any, Usage: "Metadata the answer starts with, by key.",
		Set: func(b *bidiDef, v config.Value) error { return headerValues(v, &b.header) },
	},
	bidiField{
		Key: "trailer", Kind: config.Any, Usage: "Metadata the answer ends with, by key.",
		Set: func(b *bidiDef, v config.Value) error { return headerValues(v, &b.trailer) },
	},
	bidiField{
		Key: "on", Kind: config.Any, Usage: "Rules, in order: the first whose when matches a message answers it with its response.",
		Set: func(b *bidiDef, v config.Value) error {
			return config.Items(v, func(item config.Value) error {
				r := ruleDef{dir: b.dir}
				if err := _ruleSchema.Decode(&r, item); err != nil {
					return err
				}

				if r.when == nil || r.response == nil {
					return config.Invalid(item, item.Key+" needs when and response", "")
				}

				b.rules = append(b.rules, r)

				return nil
			})
		},
	},
	bidiField{
		Key: "otherwise", Kind: config.Any, Usage: "The response to a message no rule matches.",
		Set: func(b *bidiDef, v config.Value) error {
			b.otherwise = &replyDef{dir: b.dir}
			return _replySchema.Decode(b.otherwise, v)
		},
	},
	bidiField{
		Key: "on_end", Kind: config.Any, Usage: "The response once the client closed its side.",
		Set: func(b *bidiDef, v config.Value) error {
			b.onEnd = &replyDef{dir: b.dir}
			return _replySchema.Decode(b.onEnd, v)
		},
	},
)

var _ruleSchema = config.NewSchema(
	ruleField{
		Key: "when", Kind: config.Any, Usage: "Matcher list of the message as JSON text, such as [field, message, [equal, hi]].",
		Set: func(r *ruleDef, v config.Value) error {
			m, err := matcherOf(v)
			r.when = m

			return err
		},
	},
	ruleField{
		Key: "response", Kind: config.Any, Usage: "The response to a message when matches.",
		Set: func(r *ruleDef, v config.Value) error {
			r.response = &replyDef{dir: r.dir}
			return _replySchema.Decode(r.response, v)
		},
	},
)

var _proxySchema = config.NewSchema(
	proxyField{
		Key: "target", Kind: config.String, Usage: "The server to proxy to, as grpc.NewClient takes it, such as localhost:50051. The call goes without TLS.",
		Set: func(p *proxyDef, v config.Value) error { p.target = v.Str; return nil },
	},
	proxyField{
		Key: "header", Kind: config.Any, Usage: "Metadata the target gets, by key, in place of the client's values: a string or a list of strings each.",
		Set: func(p *proxyDef, v config.Value) error { return headerValues(v, &p.header) },
	},
	proxyField{
		Key: "remove_headers", Kind: config.List, Usage: "Keys of the client's metadata the target doesn't get.",
		Set: func(p *proxyDef, v config.Value) error { p.remove = v.List; return nil },
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

// check reports the problems between keys of a decoded document that came from origin. found are the problems
// decoding already found: a key that failed to decode was still set.
func (d *mockDef) check(origin string, found []config.Problem) []config.Problem {
	var problems []config.Problem

	failed := func(key string) bool {
		return slices.ContainsFunc(found, func(p config.Problem) bool {
			return p.Key == key || strings.HasPrefix(p.Key, key+".")
		})
	}

	if d.request.method == "" && !failed("request.method") {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: origin, Key: "request.method",
			Message: "request.method is required", Hint: "name the method, such as pkg.Service/Method",
		})
	}

	problems = append(problems, conflicts(origin, "request", d.request.message)...)

	answers := d.answers()
	for _, k := range answers[min(1, len(answers)):] {
		problems = append(problems, config.Problem{
			Code: config.CodeConflict, Origin: origin, Key: k,
			Message: k + " is set with " + answers[0], Hint: "keep one of them: bidi answers bidi streaming methods, and proxy any method",
		})
	}

	if len(answers) == 0 && !failed("response") && !failed("bidi") && !failed("proxy") {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: origin, Key: "response",
			Message: "the mock has no response", Hint: "set response, bidi for a bidi streaming method, or proxy",
		})
	}

	for key, r := range d.replies() {
		problems = append(problems, conflicts(origin, key, r.keys)...)
	}

	if d.scenario.set && d.scenario.name == "" {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: origin, Key: "scenario.name",
			Message: "scenario is set but scenario.name is not", Hint: "name the scenario",
		})
	}

	return problems
}

// answers returns the keys of the answers the document sets, of response, bidi and proxy, in that order: a mock has one.
func (d *mockDef) answers() []string {
	var keys []string
	if d.reply != nil {
		keys = append(keys, "response")
	}

	if d.bidi != nil {
		keys = append(keys, "bidi")
	}

	if d.proxy != nil {
		keys = append(keys, "proxy")
	}

	return keys
}

// replies yields the responses of the document, by key.
func (d *mockDef) replies() map[string]*replyDef {
	out := make(map[string]*replyDef)
	if d.reply != nil {
		out["response"] = d.reply
	}

	if d.bidi != nil {
		for i, r := range d.bidi.rules {
			out[fmt.Sprintf("bidi.on[%d].response", i)] = r.response
		}

		if d.bidi.otherwise != nil {
			out["bidi.otherwise"] = d.bidi.otherwise
		}

		if d.bidi.onEnd != nil {
			out["bidi.on_end"] = d.bidi.onEnd
		}
	}

	return out
}

// apply sets on b what the document sets.
func (d *mockDef) apply(b *Builder) {
	b.method = d.request.method

	if d.id != "" {
		b.ID(d.id)
	}

	b.Name(d.name).Enabled(d.enabled)

	if d.hasPriority {
		b.Priority(d.priority)
	}

	if d.hasDelay {
		b.Delay(d.delay)
	}

	if d.repeat > 0 {
		b.Times(d.repeat)
	}

	if d.scenario.set {
		b.ScenarioIs(d.scenario.name)
		if d.scenario.requiredState != "" {
			b.ScenarioStateIs(d.scenario.requiredState)
		}

		if d.scenario.newState != "" {
			b.ScenarioStateWillBe(d.scenario.newState)
		}
	}

	for _, e := range d.events {
		b.OnCompleteEvent(e)
	}

	for _, set := range slices.Concat(d.request.header, d.request.fields) {
		set(b)
	}

	if d.request.json != nil {
		b.JSON(*d.request.json)
	}

	if d.request.body != nil {
		b.Body(d.request.body)
	}

	switch {
	case d.bidi != nil:
		b.Reply(d.bidi.reply())
	case d.reply != nil:
		b.Reply(d.reply.response())
	case d.proxy != nil:
		b.Reply(d.proxy.reply())
	}
}

// reply returns the ProxyReply the document describes.
func (p *proxyDef) reply() *ProxyReply {
	rep := From(p.target).RemoveHeaders(p.remove...)

	for _, h := range p.header {
		rep.Header(h.name, h.value)
	}

	return rep
}

// response returns the Response the reply describes.
func (r *replyDef) response() *Response {
	res := &Response{json: r.messages, templated: r.templated}
	if r.messages == nil {
		res.json = nil
	}

	for _, h := range r.header {
		res.Header(h.name, h.value)
	}

	for _, h := range r.trailer {
		res.Trailer(h.name, h.value)
	}

	if r.status != nil {
		res.Status(r.status.code, r.status.message)
		res.detailJSON = r.status.details
	}

	if r.hasDelay {
		res.MessageDelay(r.delay)
	}

	return res
}

// reply returns the BidiReply the document describes.
func (b *bidiDef) reply() *BidiReply {
	rep := Bidi()

	for _, h := range b.header {
		rep.Header(h.name, h.value)
	}

	for _, h := range b.trailer {
		rep.Trailer(h.name, h.value)
	}

	for _, rule := range b.rules {
		rep.On(rule.when, rule.response.response())
	}

	if b.otherwise != nil {
		rep.Otherwise(b.otherwise.response())
	}

	if b.onEnd != nil {
		rep.OnEnd(b.onEnd.response())
	}

	return rep
}

// readMessages reads the messages of body_file, v, relative to the directory of r: a JSON file of a message, or a list
// of them.
func readMessages(r *replyDef, v config.Value) error {
	path := v.Str
	if !filepath.IsAbs(path) && r.dir != "" {
		path = filepath.Join(r.dir, path)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return config.Invalid(v, fmt.Sprintf("%s: %v", v.Key, err), "a relative body_file opens from the mock file's directory")
	}

	doc, err := config.Parse(b, "json")
	if err != nil {
		return config.Invalid(v, fmt.Sprintf("%s: %s is not JSON: %v", v.Key, path, err), "")
	}

	items, ok := doc.([]any)
	if !ok {
		items = []any{doc}
	}

	r.messages = make([]string, 0, len(items))
	// Each item was read from JSON: it writes back.
	for _, item := range items {
		text, _ := json.Marshal(item)
		r.messages = append(r.messages, string(text))
	}

	return nil
}

// jsonText returns the JSON text a value holds: the text of a string, which must be JSON, else the value written as
// JSON.
func jsonText(v config.Value) (string, error) {
	if s, ok := v.Any.(string); ok {
		return s, nil
	}

	b, err := json.Marshal(v.Any)
	if err != nil {
		return "", config.Invalid(v, fmt.Sprintf("%s can't be written as JSON: %v", v.Key, err), "")
	}

	return string(b), nil
}

// codeName returns the name of c as gRPC writes it, such as NOT_FOUND.
func codeName(c codes.Code) string {
	var b strings.Builder

	prev := rune(0)
	for _, r := range c.String() {
		if 'a' <= prev && prev <= 'z' && 'A' <= r && r <= 'Z' {
			b.WriteByte('_')
		}

		b.WriteRune(r)
		prev = r
	}

	return strings.ToUpper(b.String())
}

// codeOf returns the code v names: a name such as NOT_FOUND or NotFound, in any case, or a number from 0 to 16.
func codeOf(v config.Value) (codes.Code, error) {
	switch x := v.Any.(type) {
	case int64:
		if x < 0 || x > 16 {
			return 0, config.Invalid(v, fmt.Sprintf("%s=%d is no gRPC code", v.Key, x), "a code is from 0 to 16")
		}

		return codes.Code(x), nil
	case string:
		for c := range codes.Code(17) {
			if strings.EqualFold(x, codeName(c)) || strings.EqualFold(x, c.String()) {
				return c, nil
			}
		}

		return 0, config.Invalid(v, fmt.Sprintf("%s=%q is no gRPC code", v.Key, x), "name one such as NOT_FOUND, or give its number")
	default:
		return 0, config.Mismatch(v, "a code name or number")
	}
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

// headerValues decodes an object of metadata values: a string, or a list of strings, each.
func headerValues(v config.Value, dst *[]headerValue) error {
	return config.MemberStrings(v, func(name, value string) {
		*dst = append(*dst, headerValue{name: name, value: value})
	})
}

// _builtinMatchers builds the matcher lists of mock files.
var _builtinMatchers = matchbuild.MustNew()

// matcherOf returns the matcher of v, a value of a mock file: text matches equal text, and a list is a matcher spec.
func matcherOf(v config.Value) (vlatte.Matcher, error) {
	switch x := v.Any.(type) {
	case string:
		return _builtinMatchers.TryBuildMatcher(x)
	case []any:
		if len(x) == 0 {
			return nil, config.Invalid(v, v.Key+" is an empty list", "name a matcher, such as [equal, x]")
		}

		return _builtinMatchers.TryBuildMatcher(x)
	default:
		return nil, config.Mismatch(v, "a string or a matcher list, such as [equal, x]", config.String)
	}
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

// conflicts reports each key of set after the first as set with the first: only one of them can be set.
func conflicts(origin, prefix string, set []string) []config.Problem {
	if len(set) < 2 {
		return nil
	}

	first := prefix + "." + set[0]

	problems := make([]config.Problem, 0, len(set)-1)
	for _, k := range set[1:] {
		problems = append(problems, config.Problem{
			Code: config.CodeConflict, Origin: origin, Key: prefix + "." + k,
			Message: fmt.Sprintf("%s.%s is set with %s", prefix, k, first), Hint: "keep one of them",
		})
	}

	return problems
}
