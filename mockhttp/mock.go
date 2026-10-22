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
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match"
)

var (
	_ vlatte.Mock          = (*Mock)(nil)
	_ io.Closer            = (*Mock)(nil)
	_ vlatte.CallbackInput = (*CallbackInput)(nil)
)

type CallbackInput struct {
	m   *Mock
	req *RequestValues
	res *Response
}

func (i *CallbackInput) Kind() vlatte.Kind {
	return Kind
}

func (i *CallbackInput) Mock() vlatte.Mock {
	return i.m
}

func (i *CallbackInput) Request() *RequestValues {
	return i.req
}

func (i *CallbackInput) Response() *Response {
	return i.res
}

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

	// URL is the full request url.URL, including scheme, host, port.
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
	callbacks    []vlatte.Callback
	steps        []vlatte.Step
	wireSteps    []vlatte.Step
	upSteps      []vlatte.Step
	fallback     bool // a ProxyAll mock's: its description holds its reply under proxy_all
}

// clone returns a copy of s that shares no slice with it, so changing one doesn't change the other.
// A new slice field must be cloned here too.
func (s mockSpec) clone() mockSpec {
	s.expectations = slices.Clone(s.expectations)
	s.mappers = slices.Clone(s.mappers)
	s.events = slices.Clone(s.events)
	s.callbacks = slices.Clone(s.callbacks)
	s.steps = slices.Clone(s.steps)
	s.wireSteps = slices.Clone(s.wireSteps)
	s.upSteps = slices.Clone(s.upSteps)

	return s
}

// Mock holds metadata and expectations to be matched against HTTP requests in order to serve mocked responses.
// This is the core entity of this project, and most features work based on it.
// A Builder makes it; afterwards only its enabled state and hits change.
type Mock struct {
	mu sync.RWMutex

	spec      mockSpec
	reply     Replier               // made for this mock by the ReplierFactory of its builder
	replyDesc descriptor.Descriptor // the description of that factory, as it was when the mock was built
	closer    io.Closer             // what releases the reply, when the mock owns it; nil otherwise
	closeOnce sync.Once
	enabled   bool
	hits      atomic.Int64
}

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

func (m *Mock) Name() string {
	return m.spec.name
}

func (m *Mock) Priority() int {
	return m.spec.priority
}

func (m *Mock) Origin() string {
	return m.spec.origin
}

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

func (m *Mock) Switch(state bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = state
}

func (m *Mock) Events() vlatte.Events {
	return vlatte.Events{OnComplete: m.spec.events}
}

func (m *Mock) Callbacks() []vlatte.Callback {
	return m.spec.callbacks
}

func (m *Mock) Steps() []vlatte.Step {
	return m.spec.steps
}

// WireSteps returns the steps that the bytes of each answer of the mock pass through: see Builder.WireStep.
func (m *Mock) WireSteps() []vlatte.Step {
	return m.spec.wireSteps
}

// UpstreamSteps returns the steps that what the mock sends on passes through: see Builder.UpstreamStep.
func (m *Mock) UpstreamSteps() []vlatte.Step {
	return m.spec.upSteps
}

// String names the mock as "Mock [id] name", as assertion messages do.
func (m *Mock) String() string {
	return strings.TrimSpace("Mock [" + m.ID() + "] " + m.Name())
}

func (m *Mock) Describe() descriptor.Descriptor {
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
		case matchingScopeQuery, matchingScopeQueries, matchingScopeHeader, matchingScopeFormField:
			keys := make(map[string]any, counters[target])

			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(descriptor.SelfDescriptive); ok {
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
					if sd, ok := e.Matcher.(descriptor.SelfDescriptive); ok {
						keys[e.Key] = sd.Describe()
					}
				} else {
					matchers := make([]vlatte.Matcher, 0, size)
					for _, e := range byKey[e.Key] {
						matchers = append(matchers, e.Matcher)
					}

					all := match.All(matchers...)
					if sd, ok := vlatte.Matcher(all).(descriptor.SelfDescriptive); ok {
						keys[e.Key] = sd.Describe()
					}
				}
			}

			request[target.FieldName()] = keys

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
			matchingScopeBody:
			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(descriptor.SelfDescriptive); ok {
					request[target.FieldName()] = sd.Describe()
				}

				continue
			}

			matchers := make([]vlatte.Matcher, len(expectations))
			for i, e := range expectations {
				matchers[i] = e.Matcher
			}

			all := match.All(matchers...)
			if sd, ok := vlatte.Matcher(all).(descriptor.SelfDescriptive); ok {
				request[target.FieldName()] = sd.Describe()
			}

		default:
			for _, e := range expectations {
				if sd, ok := e.Matcher.(descriptor.SelfDescriptive); ok {
					if desc := sd.Describe(); desc.IsMap() {
						desc.Merge(data)
					}
				}
			}
		}
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

	// The reply as it was when the mock was built: changing it afterwards changes only the mocks built afterwards.
	if m.replyDesc.IsMap() {
		m.replyDesc.Merge(data)
	}

	// A ProxyAll mock given another reply has no proxy reply to describe.
	if proxy, ok := data["proxy"]; ok && m.spec.fallback {
		data["proxy_all"] = proxy
		delete(data, "proxy")
	}

	if events := describeEvents(m.spec.events); len(events) > 0 {
		data["on_complete"] = events
	}

	return descriptor.Root(data)
}

// describeEvents describes events as a mock file writes them. Parameters that describe themselves, as a webhook's do,
// give their description; others, such as the maps of mock files, go as they are.
func describeEvents(events []vlatte.EventConfig) []any {
	described := make([]any, 0, len(events))
	for _, e := range events {
		event := map[string]any{"name": e.Name}

		switch p := e.Parameters.(type) {
		case nil:
		case descriptor.SelfDescriptive:
			event["parameters"] = p.Describe().Props
		default:
			event["parameters"] = p
		}

		described = append(described, event)
	}

	return described
}

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
	default:
		return ""
	}
}
