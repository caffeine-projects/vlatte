package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*Builder)(nil)

var (
	ErrNoExpectations = errors.New("mock: at least 1 request matcher must be set")
	ErrNoReplies      = errors.New("mock: no reply set. Use .Reply() or any equivalent to set the expected mock response")
)

// Builder is a default builder for Mock.
type Builder struct {
	mock                  *Mock
	scenario              string
	scenarioNewState      string
	scenarioRequiredState string
}

// Request creates a new empty Builder.
func Request() *Builder {
	return &Builder{mock: NewMock()}
}

// AnyMethod creates a new empty Builder.
func AnyMethod() *Builder {
	b := &Builder{mock: NewMock()}
	return b.MethodMatches(match.Anything())
}

// Get initializes a mock for GET method.
func Get(m vlatte.Matcher) *Builder {
	return Request().URL(m).Method(http.MethodGet)
}

// Getf initializes a mock for GET method.
func Getf(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodGet)
}

// Post initializes a mock for Post method.
func Post(m vlatte.Matcher) *Builder {
	return Request().URL(m).Method(http.MethodPost)
}

// Postf initializes a mock for Post method.
func Postf(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodPost)
}

// Put inits a mock for Put method.
func Put(m vlatte.Matcher) *Builder {
	return Request().URL(m).Method(http.MethodPut)
}

// Putf initializes a mock for Put method.
func Putf(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodPut)
}

// Patch initializes a mock for Patch method.
func Patch(u vlatte.Matcher) *Builder {
	return Request().URL(u).Method(http.MethodPatch)
}

// Patchf initializes a mock for Patch method.
func Patchf(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodPatch)
}

// Delete initializes a mock for Delete method.
func Delete(m vlatte.Matcher) *Builder {
	return Request().URL(m).Method(http.MethodDelete)
}

// Deletef initializes a mock for Delete method.
func Deletef(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodDelete)
}

// Head initializes a mock for Head method.
func Head(m vlatte.Matcher) *Builder {
	return Request().URL(m).Method(http.MethodHead)
}

// Headf initializes a mock for Head method.
func Headf(path string, a ...any) *Builder {
	return Request().URLPathf(path, a...).Method(http.MethodHead)
}

// Name defines a name for the mock.
// Useful for debugging.
func (b *Builder) Name(name string) *Builder {
	b.mock.name = name

	return b
}

// Priority sets the priority of the mock.
// A higher priority will take precedence during request matching.
func (b *Builder) Priority(p int) *Builder {
	b.mock.priority = p

	return b
}

// Scheme sets the HTTP request scheme to be matched.
func (b *Builder) Scheme(scheme string) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeScheme),
		Key:      scheme,
		Matcher:  match.EqualIgnoreCase(scheme),
		Selector: selectScheme,
	})

	return b
}

// SchemeMatches sets a matcher.Matcher for the URL scheme part.
func (b *Builder) SchemeMatches(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeScheme),
		Matcher:  m,
		Selector: selectScheme,
	})

	return b
}

// Method sets the HTTP request method to be matched.
func (b *Builder) Method(methods ...string) *Builder {
	var m vlatte.Matcher
	var key string

	if len(methods) == 0 {
		panic(".Method() requires at least one HTTP Method")
	} else if len(methods) == 1 {
		m = match.EqualIgnoreCase(methods[0])
		key = methods[0]
	} else {
		m = match.IsIn(methods)
	}

	b.appendExpectation(&expectation{
		Target:   int(matchingScopeMethod),
		Key:      key,
		Selector: selectMethod,
		Matcher:  m,
	})

	return b
}

// MethodMatches defines a matcher.Matcher for the request method.
// Useful to set a Mock for multiple HTTP Request methods.
func (b *Builder) MethodMatches(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeMethod),
		Selector: selectMethod,
		Matcher:  m,
	})

	return b
}

// URL defines a matcher to be applied to the http.Request url.URL.
func (b *Builder) URL(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeURL),
		Selector: selectURL,
		Matcher:  m,
	})

	return b
}

// URLf sets a matcher to the http.Request url.URL that compares the http.Request url.URL with the given value.
// The expected value will be formatted with the provided format specifier.
func (b *Builder) URLf(format string, a ...any) *Builder {
	return b.URL(match.GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPath defines a matcher to be applied to the url.URL path.
func (b *Builder) URLPath(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeURL),
		Selector: selectURLPath,
		Matcher:  m,
	})

	return b
}

// URLPathf sets a Matcher that compares the http.Request url.URL path with the given value, ignoring the case.
// The expected value will be formatted with the provided format specifier.
func (b *Builder) URLPathf(format string, a ...any) *Builder {
	return b.URLPath(match.GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPath defines a matcher to be applied to the url.URL path.
func (b *Builder) URLFragment(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeURL),
		Selector: selectURLFragment,
		Matcher:  m,
	})

	return b
}

// URLPathf sets a Matcher that compares the http.Request url.URL path with the given value, ignoring the case.
// The expected value will be formatted with the provided format specifier.
func (b *Builder) URLFragmentf(format string, a ...any) *Builder {
	return b.URLFragment(match.GlobMatch(fmt.Sprintf(format, a...)))
}

// Header adds a matcher to a specific http.Header key.
func (b *Builder) Header(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeHeader),
		Key:      name,
		Selector: selectHeader(name),
		Matcher:  m,
	})

	return b
}

// Headerf adds a matcher to a specific http.Header key.
func (b *Builder) Headerf(name string, value string, a ...any) *Builder {
	return b.Header(name, match.StrictEqual(fmt.Sprintf(value, a...)))
}

// ContentType sets a matcher that will pass if the HTTP request content type is equal to given value.
func (b *Builder) ContentType(value string, a ...any) *Builder {
	return b.Header(HeaderContentType, match.EQI(fmt.Sprintf(value, a...)))
}

// Query defines a matcher to a specific query.
func (b *Builder) Query(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeQuery),
		Key:      name,
		Selector: selectQuery(name),
		Matcher:  m,
	})

	return b
}

// Queryf defines a matcher to a specific query.
func (b *Builder) Queryf(name string, value string, a ...any) *Builder {
	return b.Query(name, match.StrictEqual(fmt.Sprintf(value, a...)))
}

// Queries define a matcher.Matcher for query parameters that contains multiple values.
func (b *Builder) Queries(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeQueries),
		Key:      name,
		Matcher:  m,
		Selector: selectQueries(name),
	})

	return b
}

// Body adds matchers to the HTTP request body.
// If the request contains a JSON body, you can provide multiple matchers to several fields.
// Example:
//
//	m.Body(JSONPath("name", EqualTo("test")), JSONPath("address.street", ToContains("nowhere")))
func (b *Builder) Body(matcherList ...vlatte.Matcher) *Builder {
	var m vlatte.Matcher
	if len(matcherList) == 0 {
		panic(".Body() func requires at least one matcher.Matcher")
	} else if len(matcherList) == 1 {
		m = matcherList[0]
	} else {
		m = match.All(matcherList...)
	}

	b.appendExpectation(&expectation{
		Target:   int(matchingScopeBody),
		Selector: selectBody,
		Matcher:  m,
	})

	return b
}

// FormField defines a matcher for a specific form field by its key.
func (b *Builder) FormField(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeFormField),
		Key:      name,
		Selector: selectFormField(name),
		Matcher:  m,
	})

	return b
}

// FormFieldf defines a matcher for a specific form field by its key.
func (b *Builder) FormFieldf(field string, value string, a ...any) *Builder {
	return b.FormField(field, match.StrictEqual(fmt.Sprintf(value, a...)))
}

// Times defines the total times that a mock should be served if the request matches.
func (b *Builder) Times(times int64) *Builder {
	b.appendExpectation(&expectation{
		Target:  int(matchingScopeNone),
		Matcher: mfeat.Repeat(times),
	})

	return b
}

// Once defines that a mock should be served only one time.
func (b *Builder) Once() *Builder {
	return b.Times(1)
}

// RequestMatches applies the given predicate to the incoming http.Request.
func (b *Builder) RequestMatches(predicate func(r *http.Request) (bool, error)) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeNone),
		Selector: selectRawRequest,
		Matcher: match.Function(func(v any) (bool, error) {
			r, ok := v.(*http.Request)
			if !ok {
				return false, fmt.Errorf("mock: request matcher got %T, want *http.Request", v)
			}

			return predicate(r)
		}),
	})

	return b
}

// StartScenario sets that this mock will start a new scenario with the given name.
func (b *Builder) StartScenario(name string) *Builder {
	b.scenario = name
	b.scenarioRequiredState = mfeat.ScenarioStateStarted

	return b
}

// ScenarioIs mark this mock to be used only within the given scenario.
func (b *Builder) ScenarioIs(scenario string) *Builder {
	b.scenario = scenario

	return b
}

// ScenarioStateIs mark this mock to be served only if the scenario state is equal to the given required state.
func (b *Builder) ScenarioStateIs(requiredState string) *Builder {
	b.scenarioRequiredState = requiredState

	return b
}

// ScenarioStateWillBe defines the state of the scenario after this mock is matched, making the scenario flow continue.
func (b *Builder) ScenarioStateWillBe(newState string) *Builder {
	b.scenarioNewState = newState

	return b
}

// Callback adds a callback that will be executed after the mocked response is served.
func (b *Builder) Callback(callback vlatte.Callback) *Builder {
	b.mock.CallbackList = append(b.mock.CallbackList, callback)

	return b
}

// OnCompleteEvent adds an on-complete event to the mock.
func (b *Builder) OnCompleteEvent(cfg vlatte.EventConfig) *Builder {
	b.mock.OnCompleteList = append(b.mock.OnCompleteList, cfg)
	return b
}

func (b *Builder) Step(steps ...vlatte.Step) *Builder {
	b.mock.steps = append(b.mock.steps, steps...)

	return b
}

// Delay waits for the given duration before serving the mocked response.
func (b *Builder) Delay(duration time.Duration) *Builder {
	b.mock.Delay = vlatte.Latency(duration)

	return b
}

// DelayFunc sets a function that picks the delay before each mocked response,
// such as [vlatte.Jitter] or [vlatte.UniformDistribution].
func (b *Builder) DelayFunc(delay vlatte.Delay) *Builder {
	b.mock.Delay = delay

	return b
}

// Map adds a Mapper that allows modifying the response after it was built.
// Multiple mappers can be added.
func (b *Builder) Map(mapper Mapper) *Builder {
	b.mock.Mappers = append(b.mock.Mappers, mapper)

	return b
}

// Reply sets the replier to be used when this mock matches a request.
func (b *Builder) Reply(rep Replier) *Builder {
	b.mock.Reply = rep
	return b
}

// Enabled define if the Mock will be enabled or disabled.
// All mocks are enabled by default.
func (b *Builder) Enabled(enabled bool) *Builder {
	b.mock.Switch(enabled)

	return b
}

// SetSource sets the source of the Mock.
// This could be a filename or any relevant information about the source of the Mock.
// This is mostly used internally.
func (b *Builder) SetSource(src string) *Builder {
	b.mock.origin = src

	return b
}

// Build builds a Mock with previously configured parameters.
// Used internally by Server.
func (b *Builder) Build(app *Server) (*Mock, error) {
	if len(b.mock.expectations) == 0 {
		return nil, ErrNoExpectations
	}

	if b.mock.Reply == nil {
		return nil, ErrNoReplies
	}

	if r, ok := b.mock.Reply.(replyOnBeforeBuild); ok {
		err := r.beforeBuild(app)
		if err != nil {
			return nil, err
		}
	}

	if b.scenario != "" {
		b.appendExpectation(&expectation{
			Target:  int(matchingScopeScenario),
			Matcher: mfeat.Scenario(app.scenarioStore, b.scenario, b.scenarioRequiredState, b.scenarioNewState),
		})
	}

	for i, def := range b.mock.Events().OnComplete {
		if app.config.Listeners == nil || !app.config.Listeners.OnComplete.Has(def.Name) {
			return nil, fmt.Errorf("mock: on_complete listener %s at index %d is not registered", def.Name, i)
		}
	}

	return b.mock, nil
}

func (b *Builder) appendExpectation(e *vlatte.Eval[*selectorInput]) {
	b.mock.expectations = append(b.mock.expectations, e)
}

// --
// Request Values Selectors
// --

func selectScheme(_ context.Context, r *selectorInput) any { return r.URL.Scheme }

func selectMethod(_ context.Context, r *selectorInput) any      { return r.RawRequest.Method }
func selectURL(_ context.Context, r *selectorInput) any         { return r.URL.String() }
func selectURLPath(_ context.Context, r *selectorInput) any     { return r.URL.Path }
func selectURLFragment(_ context.Context, r *selectorInput) any { return r.URL.Fragment }
func selectHeader(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.RawRequest.Header.Get(k) }
}

func selectQuery(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.Query.Get(k) }
}

func selectQueries(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.Query[k] }
}
func selectBody(_ context.Context, r *selectorInput) any { return r.ParsedBody }
func selectFormField(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.RawRequest.Form.Get(k) }
}
func selectRawRequest(_ context.Context, r *selectorInput) any { return r.RawRequest }
