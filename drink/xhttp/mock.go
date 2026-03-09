package xhttp

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
	"github.com/caffeine-projects/vlatte/shelf/kv"
)

var _ vlatte.Bean = (*HTTPMock)(nil)
var _ vlatte.CallbackInput = (*HTTPCallbackInput)(nil)

type HTTPCallbackInput struct {
	m   *HTTPMock
	req *RequestValues
	res *Response
}

func (i *HTTPCallbackInput) Kind() vlatte.Kind {
	return vlatte.KindHTTP
}

func (i *HTTPCallbackInput) Bean() vlatte.Bean {
	return i.m
}

func (i *HTTPCallbackInput) Request() *RequestValues {
	return i.req
}

func (i *HTTPCallbackInput) Response() *Response {
	return i.res
}

// Mapper is the function definition to be used to map a Mock response MockedResponse before serving it.
type Mapper func(requestValues *RequestValues, res *Response) error

// MockFileHandler defines a custom Mock file configuration handler.
// It lets users define custom fields on mock configuration files that could be handled by a MockFileHandler instance.
// It is also possible to change how built-in fields are handled.
type MockFileHandler interface {
	// Handle handles a Mock configuration file.
	//  Parameter fields
	Handle(fields map[string]any, b *HTTPMockBuilder) error
}

// mockFileData represents the data that is passed to Mock files during template parsing.
type mockFileData struct {
	params  kv.KV
	data    map[string]any
	baseURL string
}

func (t *mockFileData) Parameters() kv.KV {
	return t.params
}

func (t *mockFileData) Data() map[string]any {
	return t.data
}

func (t *mockFileData) URL(paths ...string) string {
	if len(paths) == 0 {
		return t.baseURL
	}

	u, err := url.JoinPath(t.baseURL, paths...)
	if err != nil {
		return ""
	}

	return u
}

// HTTPValueSelector defines a function that will be used to extract the value that will be passed to the associated matcher.
type HTTPValueSelector func(ctx context.Context, r *HTTPValueSelectorInput) any

type HTTPValueSelectorInput struct {
	// RawRequest is the original incoming http.Request.
	RawRequest *http.Request

	// URL is the full request url.URL, including scheme, host, port.
	URL *url.URL

	Query url.Values

	Form url.Values

	// ParsedBody is the parsed http.Request body.
	ParsedBody any
}

type HTTPExpectation = vlatte.Eval[*HTTPValueSelectorInput]

// HTTPMock holds metadata and expectations to be matched against HTTP requests in order to serve mocked responses.
// This is the core entity of this project, and most features work based on it.
type HTTPMock struct {
	mu sync.RWMutex

	id           string
	name         string
	priority     int
	origin       string
	hits         atomic.Int64
	enabled      bool
	expectations []*vlatte.Eval[*HTTPValueSelectorInput]
	ext          any

	Reply Reply

	// Mappers stores response mappers associated with this Mock.
	Mappers []Mapper

	// Delay sets the duration to delay serving the mocked response.
	Delay vlatte.Delay

	OnCompleteList []vlatte.EventConfig

	CallbackList []vlatte.Callback

	steps []vlatte.Step
}

// NewMock returns a new Mock with default values set.
func NewMock() *HTTPMock {
	return &HTTPMock{
		id:           uuid.New().String(),
		expectations: make([]*vlatte.Eval[*HTTPValueSelectorInput], 0),
		enabled:      true,

		OnCompleteList: make([]vlatte.EventConfig, 0),
		CallbackList:   make([]vlatte.Callback, 0),
		steps:          make([]vlatte.Step, 0),
		Mappers:        make([]Mapper, 0),
	}
}

func (m *HTTPMock) ID() string {
	return m.id
}

func (m *HTTPMock) Name() string {
	return m.name
}

func (m *HTTPMock) Priority() int {
	return m.priority
}

func (m *HTTPMock) Origin() string {
	return m.origin
}

func (m *HTTPMock) Ext() any {
	return m.ext
}

func (m *HTTPMock) Roasted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *HTTPMock) Inc() {
	m.hits.Add(1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *HTTPMock) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *HTTPMock) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *HTTPMock) Roast() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *HTTPMock) Raw() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

func (m *HTTPMock) Switch(state bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = state
}

func (m *HTTPMock) Events() vlatte.Events {
	return vlatte.Events{OnComplete: m.OnCompleteList}
}

func (m *HTTPMock) Callbacks() []vlatte.Callback {
	return m.CallbackList
}

func (m *HTTPMock) Steps() []vlatte.Step {
	return m.steps
}

func (m *HTTPMock) String() string {
	// TODO: implement me.
	return ""
}

func (m *HTTPMock) Describe() descriptor.Descriptor {
	mockExpectations := m.expectations
	data := make(map[string]any)
	request := make(map[string]any)
	counters := make(map[matchingScope]int)

	for _, v := range mockExpectations {
		counters[matchingScope(v.Target)]++
	}

	expectationsByTarget := make(map[matchingScope][]*HTTPExpectation)
	for _, v := range mockExpectations {
		t := matchingScope(v.Target)
		expectationsByTarget[t] = append(expectationsByTarget[t], v)
	}

	for target, expectations := range expectationsByTarget {
		switch target {
		case matchingScopeQuery, matchingScopeQueries, matchingScopeHeader, matchingScopeFormField:
			keys := make(map[string]any, counters[target])

			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(descriptor.Self); ok {
					keys[expectations[0].Key] = sd.Describe()
					request[target.FieldName()] = keys
				}

				continue
			}

			byKey := make(map[string][]*HTTPExpectation, counters[target])
			for _, e := range expectations {
				byKey[e.Key] = append(byKey[e.Key], e)
			}

			for _, e := range expectations {
				size := len(byKey[e.Key])
				if size == 0 {
					continue
				}

				if size == 1 {
					if sd, ok := e.Matcher.(descriptor.Self); ok {
						keys[e.Key] = sd.Describe()
					}
				} else {
			matchers := make([]vlatte.Filter, 0, size)
					for _, e := range byKey[e.Key] {
						matchers = append(matchers, e.Matcher)
					}

					all := filter.All(matchers...)
					keys[e.Key] = all.(descriptor.Self).Describe()
				}
			}

			request[target.FieldName()] = keys

		case matchingScopeScheme, matchingScopeURL, matchingScopeURLPath, matchingScopeBody:
			if len(expectations) == 1 {
				if sd, ok := expectations[0].Matcher.(descriptor.Self); ok {
					request[target.FieldName()] = sd.Describe()
				}

				continue
			}

			matchers := make([]vlatte.Filter, len(expectations))
			for i, e := range expectations {
				matchers[i] = e.Matcher
			}

			all := filter.All(matchers...)
			request[target.FieldName()] = all.(descriptor.Self).Describe()

		default:
			for _, e := range expectations {
				if sd, ok := e.Matcher.(descriptor.Self); ok {
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

	if m.Priority() != 0 {
		data["priority"] = m.Priority()
	}

	if len(request) > 0 {
		data["request"] = request
	}

	if m.Reply != nil {
		if sd, ok := m.Reply.(descriptor.Self); ok {
			if desc := sd.Describe(); desc.IsMap() {
				desc.Merge(data)
			}
		}
	}

	return descriptor.Root(data)
}

func (m *HTTPMock) MarshalJSON() ([]byte, error) {
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
	matchingScopeScenario
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
		return "form_url_encoded"
	case matchingScopeScenario:
		return "scenario"
	default:
		return ""
	}
}

func (t matchingScope) String() string {
	switch t {
	case matchingScopeNone:
		return "Req"
	case matchingScopeScheme:
		return "Scheme"
	case matchingScopeMethod:
		return "Method"
	case matchingScopeURL:
		return "URL"
	case matchingScopeURLPath:
		return "URLPath"
	case matchingScopeHeader:
		return "Header"
	case matchingScopeQuery, matchingScopeQueries:
		return "Query"
	case matchingScopeBody:
		return "Body"
	case matchingScopeFormField:
		return "Form"
	case matchingScopeScenario:
		return "Scenario"
	default:
		return "Req"
	}
}
