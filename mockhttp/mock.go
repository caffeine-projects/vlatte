package mockhttp

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/delay"
	"github.com/caffeine-projects/vlatte/match"
)

var (
	_ vlatte.Mock = (*Mock)(nil)
	_ io.Closer   = (*Mock)(nil)
)

// Mapper is the function definition to be used to map a Mock response MockedResponse before serving it.
// The response is the mapper's to change, but for its Body bytes, which other responses of the mock can share: replace
// them, don't change them in place.
type Mapper func(requestValues *RequestValues, res *Response) error

// MockFileHandler reads the ext object of mock files, so users can add settings of their own to mock files.
type MockFileHandler interface {
	// Handle reads fields, the ext object of a mock file, and sets the mock through b.
	// Keys are as written. Objects are map[string]any and lists []any; whole numbers that fit int64 are int64,
	// and other numbers float64, from JSON and YAML alike.
	// An error is a problem of the mock file's ext key.
	Handle(fields map[string]any, b *Builder) error
}

type selectorInput struct {
	// RawRequest is the original incoming http.Request.
	RawRequest *http.Request

	// URL is the full request url.URL, including scheme, host, port: those a request in absolute form, as a forward
	// proxy gets, or an intercepted one names, and the server's otherwise.
	URL *url.URL

	Query url.Values

	// Form is the form of a form body, urlencoded or multipart, without the query: http.Request.PostForm.
	Form url.Values

	// Files are the files of a multipart form body, by field name: http.Request.MultipartForm.File.
	Files map[string][]*multipart.FileHeader

	// ParsedBody is the parsed http.Request body.
	ParsedBody any

	// ForProxy is set on a request the server's forward proxy sends on to the host it names: a CONNECT, or a request
	// in absolute form.
	ForProxy bool

	// values are the values of the request, which the key of a scenario renders with.
	values *RequestValues
}

type expectation = vlatte.Eval[*selectorInput]

// mockSpec is what a Builder sets and its mocks serve. Build gives each mock a copy of its own: see clone.
type mockSpec struct {
	id           string
	name         string
	priority     int
	origin       string
	expectations []*expectation
	mappers      []Mapper
	delay        vlatte.Delay
	events       []vlatte.EventConfig
	pipes        []vlatte.Pipe
	wirePipes    []vlatte.Pipe
	upPipes      []vlatte.Pipe
	fallback     bool          // a ProxyAll mock's: its description holds its reply under proxy_all
	pathTemplate *pathTemplate // set with URLPathTemplate; it never changes, so the mocks share it
}

// clone returns a copy of s that shares no slice with it, so changing one doesn't change the other.
// A new slice field must be cloned here too.
func (s mockSpec) clone() mockSpec {
	s.expectations = slices.Clone(s.expectations)
	s.mappers = slices.Clone(s.mappers)
	s.events = slices.Clone(s.events)
	s.pipes = slices.Clone(s.pipes)
	s.wirePipes = slices.Clone(s.wirePipes)
	s.upPipes = slices.Clone(s.upPipes)

	return s
}

// Mock holds metadata and expectations to be matched against HTTP requests in order to serve mocked responses.
// This is the core entity of this project, and most features work based on it.
// A Builder makes it; afterwards only its enabled state and hits change.
type Mock struct {
	mu sync.RWMutex

	spec      mockSpec
	reply     Replier           // made for this mock by the ReplierFactory of its builder
	replyDesc vlatte.Descriptor // the description of that factory, as it was when the mock was built
	closer    io.Closer         // what releases the reply, when the mock owns it; nil otherwise
	closeOnce sync.Once
	enabled   bool
	hits      atomic.Int64
}

// ID returns the ID of the mock: the one set with [Builder.ID], or one generated when it was built.
func (m *Mock) ID() string {
	return m.spec.id
}

// Close releases what the mock's reply holds, such as the idle connections of a proxy's HTTP client. The store closes
// a mock when it leaves the server, and Server.Close closes the mocks still stored. Only the first call does anything.
func (m *Mock) Close() error {
	var err error

	m.closeOnce.Do(func() {
		if m.closer != nil {
			err = m.closer.Close()
		}
	})

	return err
}

// Name returns the name of the mock, or "" when it has none.
func (m *Mock) Name() string {
	return m.spec.name
}

// Priority returns the priority of the mock: mocks with a lower one match first.
func (m *Mock) Priority() int {
	return m.spec.priority
}

// Origin returns where the mock comes from, such as the mock file that describes it, or "" for a mock built in Go.
func (m *Mock) Origin() string {
	return m.spec.origin
}

// Enabled reports whether the mock may match requests.
func (m *Mock) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *Mock) Inc() {
	m.hits.Add(1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *Mock) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *Mock) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *Mock) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *Mock) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

// Switch enables the mock when state is true, and disables it otherwise.
func (m *Mock) Switch(state bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = state
}

// Events returns the events the mock runs once it replied, its callbacks included.
func (m *Mock) Events() vlatte.Events {
	return vlatte.Events{OnComplete: m.spec.events}
}

// Pipes returns the pipes that the body of each answer of the mock passes through: see [Builder.Pipe].
func (m *Mock) Pipes() []vlatte.Pipe {
	return m.spec.pipes
}

// WirePipes returns the pipes that the bytes of each answer of the mock pass through: see [Builder.WirePipe].
func (m *Mock) WirePipes() []vlatte.Pipe {
	return m.spec.wirePipes
}

// UpstreamPipes returns the pipes that what the mock sends on passes through: see [Builder.UpstreamPipe].
func (m *Mock) UpstreamPipes() []vlatte.Pipe {
	return m.spec.upPipes
}

// String names the mock as "Mock [id] name", as assertion messages do.
func (m *Mock) String() string {
	return strings.TrimSpace("Mock [" + m.ID() + "] " + m.Name())
}

// Describe implements [vlatte.SelfDescriptive]: the mock in the mock file format. It leaves out callbacks, and
// pipes that don't describe themselves. Delay description errors propagate. A reply a mock file can't hold, such as [Echo], stays, so encoding the
// description fails.
func (m *Mock) Describe() vlatte.Descriptor {
	mockExpectations := m.spec.expectations
	data := make(map[string]any)
	request := make(map[string]any)
	counters := make(map[matchingScope]int)

	for _, v := range mockExpectations {
		counters[matchingScope(v.Target)]++
	}

	expectationsByTarget := make(map[matchingScope][]*expectation)
	for _, v := range mockExpectations {
		t := matchingScope(v.Target)
		expectationsByTarget[t] = append(expectationsByTarget[t], v)
	}

	// A mock file matches the URL with url or path, not both: with url matchers, the path matchers go in url, as
	// urlpath matchers, which apply them to the path of the URL.
	if paths := expectationsByTarget[matchingScopeURLPath]; len(paths) > 0 && len(expectationsByTarget[matchingScopeURL]) > 0 {
		for _, e := range paths {
			expectationsByTarget[matchingScopeURL] = append(expectationsByTarget[matchingScopeURL],
				&expectation{Target: int(matchingScopeURL), Matcher: match.URLPathMatch(e.Matcher)})
		}

		delete(expectationsByTarget, matchingScopeURLPath)
	}

	for target, expectations := range expectationsByTarget {
		switch target {
		case matchingScopeQuery, matchingScopeQueries, matchingScopeHeader, matchingScopeHeaders, matchingScopeFormField,
			matchingScopeFormFields, matchingScopeCookie, matchingScopeBasicAuth, matchingScopePathParam:
			keys := make(map[string]any, counters[target])

			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(vlatte.SelfDescriptive); ok {
					keys[expectations[0].Key] = sd.Describe()
					request[target.FieldName()] = keys
				}

				continue
			}

			byKey := make(map[string][]*expectation, counters[target])
			for _, e := range expectations {
				byKey[e.Key] = append(byKey[e.Key], e)
			}

			for _, e := range expectations {
				size := len(byKey[e.Key])
				if size == 0 {
					continue
				}

				if size == 1 {
					if sd, ok := e.Matcher.(vlatte.SelfDescriptive); ok {
						keys[e.Key] = sd.Describe()
					}
				} else {
					matchers := make([]vlatte.Matcher, 0, size)
					for _, e := range byKey[e.Key] {
						matchers = append(matchers, e.Matcher)
					}

					all := match.All(matchers...)
					if sd, ok := vlatte.Matcher(all).(vlatte.SelfDescriptive); ok {
						keys[e.Key] = sd.Describe()
					}
				}
			}

			request[target.FieldName()] = keys

		case matchingScopeURLPathTemplate:
			// A mock has one path template, which describes as it was written.
			request[target.FieldName()] = expectations[0].Key

		case matchingScopeFormFile:
			// One pattern of a field is an object, and several a list, in the order they were added.
			files := make(map[string]any)
			for _, e := range expectations {
				desc := describeMatcher(e.Matcher)

				switch prev := files[e.Key].(type) {
				case nil:
					files[e.Key] = desc
				case []any:
					files[e.Key] = append(prev, desc)
				default:
					files[e.Key] = []any{prev, desc}
				}
			}

			request[target.FieldName()] = files

		case matchingScopeScheme, matchingScopeMethod, matchingScopeURL, matchingScopeURLPath, matchingScopeURLFragment,
			matchingScopeBody, matchingScopeHost, matchingScopeClientIP:
			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(vlatte.SelfDescriptive); ok {
					request[target.FieldName()] = sd.Describe()
				}

				continue
			}

			matchers := make([]vlatte.Matcher, len(expectations))
			for i, e := range expectations {
				matchers[i] = e.Matcher
			}

			all := match.All(matchers...)
			if sd, ok := vlatte.Matcher(all).(vlatte.SelfDescriptive); ok {
				request[target.FieldName()] = sd.Describe()
			}

		case matchingScopeCustom:
			// A list of the matchers that describe themselves, as request.custom lists them.
			var custom []any
			for _, e := range expectations {
				if sd, ok := e.Matcher.(vlatte.SelfDescriptive); ok {
					custom = append(custom, sd.Describe())
				}
			}

			if len(custom) > 0 {
				request[target.FieldName()] = custom
			}

		case matchingScopeScenario, matchingScopeRepeat:
			// They describe as a single argument, the value of their key, such as repeat: 1.
			for _, e := range expectations {
				if sd, ok := e.Matcher.(vlatte.SelfDescriptive); ok {
					if args, ok := sd.Describe().Props.([]any); ok && len(args) == 1 {
						data[target.FieldName()] = args[0]
					}
				}
			}

		default:
			for _, e := range expectations {
				if sd, ok := e.Matcher.(vlatte.SelfDescriptive); ok {
					if desc := sd.Describe(); desc.IsMap() {
						desc.Merge(data)
					}
				}
			}
		}
	}

	if m.spec.delay != nil {
		data["delay"] = delay.Doc(m.spec.delay)
	}

	data["id"] = m.ID()

	if len(m.Name()) > 0 {
		data["name"] = m.Name()
	}

	// A fallback, as ProxyAll's mock, without a priority loads back last, at math.MaxInt: its 0 goes too.
	if m.Priority() != 0 || m.spec.fallback {
		data["priority"] = m.Priority()
	}

	// Enabled is the default: a disabled mock, such as one the admin API turned off, says so.
	if !m.Enabled() {
		data["enabled"] = false
	}

	if len(request) > 0 {
		data["request"] = request
	}

	// The reply as it was when the mock was built: changing it afterwards changes only the mocks built afterwards. One
	// a mock file can't hold, such as a Handler, stays, so encoding the description fails rather than drop it.
	switch {
	case m.replyDesc.IsMap():
		m.replyDesc.Merge(data)
	case m.replyDesc.Err() != nil:
		data[m.replyDesc.Key] = m.replyDesc
	}

	// A ProxyAll mock given another reply has no proxy reply to describe.
	if proxy, ok := data["proxy"]; ok && m.spec.fallback {
		data["proxy_all"] = proxy
		delete(data, "proxy")
	}

	if events := describeEvents(m.spec.events); len(events) > 0 {
		data["on_complete"] = events
	}

	for key, pipes := range map[string][]vlatte.Pipe{
		"pipes":          m.spec.pipes,
		"wire_pipes":     m.spec.wirePipes,
		"upstream_pipes": m.spec.upPipes,
	} {
		if described := describePipes(pipes); len(described) > 0 {
			data[key] = described
		}
	}

	return vlatte.DescribeRoot(data)
}

// describeEvents describes events as a mock file writes them, but those that carry their listener, such as callbacks,
// which a mock file can't hold. Parameters that describe themselves, as a webhook's do,
// give their description; others, such as the maps of mock files, go as they are.
func describeEvents(events []vlatte.EventConfig) []any {
	described := make([]any, 0, len(events))
	for _, e := range events {
		if e.Listener != nil {
			continue
		}

		event := map[string]any{"name": e.Name}

		switch p := e.Parameters.(type) {
		case nil:
		case vlatte.SelfDescriptive:
			event["parameters"] = p.Describe().Props
		default:
			event["parameters"] = p
		}

		if e.AfterResponse {
			event["after_response"] = true
		}

		described = append(described, event)
	}

	return described
}

// describePipes describes pipes as a mock file lists them, but those that don't describe themselves, such as a
// [vlatte.PipeFunc], which a mock file can't hold. Faults describe themselves.
func describePipes(pipes []vlatte.Pipe) []any {
	var described []any
	for _, s := range pipes {
		if d, ok := vlatte.IsSelfDescriptive(s); ok {
			described = append(described, d)
		}
	}

	return described
}

// MarshalJSON implements [encoding/json.Marshaler]: it encodes the mock's [Mock.Describe] description, and fails when
// the description holds a reply a mock file can't, such as [Echo].
func (m *Mock) MarshalJSON() ([]byte, error) {
	return m.Describe().MarshalJSON()
}

type matchingScope int

const (
	matchingScopeNone matchingScope = iota
	matchingScopeScheme
	matchingScopeMethod
	matchingScopeURL
	matchingScopeURLPath
	matchingScopeHeader
	matchingScopeQuery
	matchingScopeQueries
	matchingScopeBody
	matchingScopeFormField
	matchingScopeFormFile
	matchingScopeScenario
	matchingScopeURLFragment
	matchingScopeHeaders
	matchingScopeFormFields
	matchingScopeCookie
	matchingScopeBasicAuth
	matchingScopeHost
	matchingScopeClientIP
	matchingScopeURLPathTemplate
	matchingScopePathParam
	matchingScopeRepeat
	matchingScopeCustom
)

func (t matchingScope) FieldName() string {
	switch t {
	case matchingScopeScheme:
		return "scheme"
	case matchingScopeMethod:
		return "method"
	case matchingScopeURL:
		return "url"
	case matchingScopeURLPath:
		return "path"
	case matchingScopeHeader:
		return "header"
	case matchingScopeQuery:
		return "query"
	case matchingScopeQueries:
		return "queries"
	case matchingScopeBody:
		return "body"
	case matchingScopeFormField:
		return "form"
	case matchingScopeFormFile:
		return "form_file"
	case matchingScopeScenario:
		return "scenario"
	case matchingScopeURLFragment:
		return "fragment"
	case matchingScopeHeaders:
		return "headers"
	case matchingScopeFormFields:
		return "forms"
	case matchingScopeCookie:
		return "cookie"
	case matchingScopeBasicAuth:
		return "basic_auth"
	case matchingScopeHost:
		return "host"
	case matchingScopeClientIP:
		return "client_ip"
	case matchingScopeURLPathTemplate:
		return "path_template"
	case matchingScopePathParam:
		return "path_params"
	case matchingScopeRepeat:
		return "repeat"
	case matchingScopeCustom:
		return "custom"
	default:
		return ""
	}
}
