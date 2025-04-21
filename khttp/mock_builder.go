package khttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/kcore/flow"
	"github.com/caffeine-projects/vlatte/khttp/httpval"
	"github.com/caffeine-projects/vlatte/matcher"
	"github.com/caffeine-projects/vlatte/matcher/mfeat"
)

var _ kcore.Builder[*HTTPMock, *HTTPMockApp] = (*HTTPMockBuilder)(nil)

var (
	ErrNoExpectations = errors.New("mock: at least 1 request matcher must be set")
	ErrNoReplies      = errors.New("mock: no reply set. Use .Reply() or any equivalent to set the expected mock response")
)

// HTTPMockBuilder is a default builder for Mock.
type HTTPMockBuilder struct {
	mock                  *HTTPMock
	scenario              string
	scenarioNewState      string
	scenarioRequiredState string
}

// Request creates a new empty Builder.
func Request() *HTTPMockBuilder {
	return &HTTPMockBuilder{mock: newMock()}
}

// AnyMethod creates a new empty Builder.
func AnyMethod() *HTTPMockBuilder {
	b := &HTTPMockBuilder{mock: newMock()}
	return b.MethodMatches(matcher.Anything())
}

// Get initializes a mock for GET method.
func Get(m matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(m).Method(http.MethodGet)
}

// Getf initializes a mock for GET method.
func Getf(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodGet)
}

// Post initializes a mock for Post method.
func Post(m matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(m).Method(http.MethodPost)
}

// Postf initializes a mock for Post method.
func Postf(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodPost)
}

// Put inits a mock for Put method.
func Put(m matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(m).Method(http.MethodPut)
}

// Putf initializes a mock for Put method.
func Putf(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodPut)
}

// Patch initializes a mock for Patch method.
func Patch(u matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(u).Method(http.MethodPatch)
}

// Patchf initializes a mock for Patch method.
func Patchf(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodPatch)
}

// Delete initializes a mock for Delete method.
func Delete(m matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(m).Method(http.MethodDelete)
}

// Deletef initializes a mock for Delete method.
func Deletef(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodDelete)
}

// Head initializes a mock for Head method.
func Head(m matcher.Matcher) *HTTPMockBuilder {
	return Request().URL(m).Method(http.MethodHead)
}

// Headf initializes a mock for Head method.
func Headf(path string, a ...any) *HTTPMockBuilder {
	return Request().URLPathf(path, a...).Method(http.MethodHead)
}

// Name defines a name for the mock.
// Useful for debugging.
func (b *HTTPMockBuilder) Name(name string) *HTTPMockBuilder {
	b.mock.Name = name

	return b
}

// Priority sets the priority of the mock.
// A higher priority will take precedence during request matching.
func (b *HTTPMockBuilder) Priority(p int) *HTTPMockBuilder {
	b.mock.Priority = p

	return b
}

// Scheme sets the HTTP request scheme to be matched.
func (b *HTTPMockBuilder) Scheme(scheme string) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeScheme),
		Key:      scheme,
		Matcher:  matcher.EqualIgnoreCase(scheme),
		Selector: selectScheme,
	})

	return b
}

// SchemeMatches sets a matcher.Matcher for the URL scheme part.
func (b *HTTPMockBuilder) SchemeMatches(m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeScheme),
		Matcher:  m,
		Selector: selectScheme,
	})

	return b
}

// Method sets the HTTP request method to be matched.
func (b *HTTPMockBuilder) Method(methods ...string) *HTTPMockBuilder {
	var m matcher.Matcher
	var key string

	if len(methods) == 0 {
		panic(".Method() requires at least one HTTP Method")
	} else if len(methods) == 1 {
		m = matcher.EqualIgnoreCase(methods[0])
		key = methods[0]
	} else {
		m = matcher.IsIn(methods)
	}

	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeMethod),
		Key:      key,
		Selector: selectMethod,
		Matcher:  m,
	})

	return b
}

// MethodMatches defines a matcher.Matcher for the request method.
// Useful to set a Mock for multiple HTTP Request methods.
func (b *HTTPMockBuilder) MethodMatches(m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeMethod),
		Selector: selectMethod,
		Matcher:  m,
	})

	return b
}

// URL defines a matcher to be applied to the http.Request url.URL.
func (b *HTTPMockBuilder) URL(m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeURL),
		Selector: selectURL,
		Matcher:  m,
	})

	return b
}

// URLf sets a matcher to the http.Request url.URL that compares the http.Request url.URL with the given value.
// The expected value will be formatted with the provided format specifier.
func (b *HTTPMockBuilder) URLf(format string, a ...any) *HTTPMockBuilder {
	return b.URL(matcher.GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPath defines a matcher to be applied to the url.URL path.
func (b *HTTPMockBuilder) URLPath(m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeURL),
		Selector: selectURLPath,
		Matcher:  m,
	})

	return b
}

// URLPathf sets a Matcher that compares the http.Request url.URL path with the given value, ignoring the case.
// The expected value will be formatted with the provided format specifier.
func (b *HTTPMockBuilder) URLPathf(format string, a ...any) *HTTPMockBuilder {
	return b.URLPath(matcher.GlobMatch(fmt.Sprintf(format, a...)))
}

// URLPath defines a matcher to be applied to the url.URL path.
func (b *HTTPMockBuilder) URLFragment(m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeURL),
		Selector: selectURLFragment,
		Matcher:  m,
	})

	return b
}

// URLPathf sets a Matcher that compares the http.Request url.URL path with the given value, ignoring the case.
// The expected value will be formatted with the provided format specifier.
func (b *HTTPMockBuilder) URLFragmentf(format string, a ...any) *HTTPMockBuilder {
	return b.URLFragment(matcher.GlobMatch(fmt.Sprintf(format, a...)))
}

// Header adds a matcher to a specific http.Header key.
func (b *HTTPMockBuilder) Header(name string, m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeHeader),
		Key:      name,
		Selector: selectHeader(name),
		Matcher:  m,
	})

	return b
}

// Headerf adds a matcher to a specific http.Header key.
func (b *HTTPMockBuilder) Headerf(name string, value string, a ...any) *HTTPMockBuilder {
	return b.Header(name, matcher.StrictEqual(fmt.Sprintf(value, a...)))
}

// ContentType sets a matcher that will pass if the HTTP request content type is equal to given value.
func (b *HTTPMockBuilder) ContentType(value string, a ...any) *HTTPMockBuilder {
	return b.Header(httpval.HeaderContentType, matcher.Eqi(fmt.Sprintf(value, a...)))
}

// Query defines a matcher to a specific query.
func (b *HTTPMockBuilder) Query(name string, m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeQuery),
		Key:      name,
		Selector: selectQuery(name),
		Matcher:  m,
	})

	return b
}

// Queryf defines a matcher to a specific query.
func (b *HTTPMockBuilder) Queryf(name string, value string, a ...any) *HTTPMockBuilder {
	return b.Query(name, matcher.StrictEqual(fmt.Sprintf(value, a...)))
}

// Queries define a matcher.Matcher for query parameters that contains multiple values.
func (b *HTTPMockBuilder) Queries(name string, m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
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
func (b *HTTPMockBuilder) Body(matcherList ...matcher.Matcher) *HTTPMockBuilder {
	var m matcher.Matcher
	if len(matcherList) == 0 {
		panic(".Body() func requires at least one matcher.Matcher")
	} else if len(matcherList) == 1 {
		m = matcherList[0]
	} else {
		m = matcher.All(matcherList...)
	}

	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeBody),
		Selector: selectBody,
		Matcher:  m,
	})

	return b
}

// FormField defines a matcher for a specific form field by its key.
func (b *HTTPMockBuilder) FormField(name string, m matcher.Matcher) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeFormField),
		Key:      name,
		Selector: selectFormField(name),
		Matcher:  m,
	})

	return b
}

// FormFieldf defines a matcher for a specific form field by its key.
func (b *HTTPMockBuilder) FormFieldf(field string, value string, a ...any) *HTTPMockBuilder {
	return b.FormField(field, matcher.StrictEqual(fmt.Sprintf(value, a...)))
}

// Times defines the total times that a mock should be served if the request matches.
func (b *HTTPMockBuilder) Times(times int64) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:  int(matchingScopeNone),
		Matcher: mfeat.Repeat(times),
	})

	return b
}

// Once defines that a mock should be served only one time.
func (b *HTTPMockBuilder) Once() *HTTPMockBuilder {
	return b.Times(1)
}

// RequestMatches applies the given predicate to the incoming http.Request.
func (b *HTTPMockBuilder) RequestMatches(predicate func(r *http.Request) (bool, error)) *HTTPMockBuilder {
	b.appendExpectation(&HTTPExpectation{
		Target:   int(matchingScopeNone),
		Selector: selectRawRequest,
		Matcher:  matcher.Function(func(v any) (bool, error) { return predicate(v.(*http.Request)) }),
	})

	return b
}

// StartScenario sets that this mock will start a new scenario with the given name.
func (b *HTTPMockBuilder) StartScenario(name string) *HTTPMockBuilder {
	b.scenario = name
	b.scenarioRequiredState = mfeat.ScenarioStateStarted

	return b
}

// ScenarioIs mark this mock to be used only within the given scenario.
func (b *HTTPMockBuilder) ScenarioIs(scenario string) *HTTPMockBuilder {
	b.scenario = scenario

	return b
}

// ScenarioStateIs mark this mock to be served only if the scenario state is equal to the given required state.
func (b *HTTPMockBuilder) ScenarioStateIs(requiredState string) *HTTPMockBuilder {
	b.scenarioRequiredState = requiredState

	return b
}

// ScenarioStateWillBe defines the state of the scenario after this mock is matched, making the scenario flow continue.
func (b *HTTPMockBuilder) ScenarioStateWillBe(newState string) *HTTPMockBuilder {
	b.scenarioNewState = newState

	return b
}

// Callback adds a callback that will be executed after the mocked response is served.
func (b *HTTPMockBuilder) Callback(callback kcore.Callback) *HTTPMockBuilder {
	b.mock.CallbackList = append(b.mock.CallbackList, callback)

	return b
}

func (b *HTTPMockBuilder) PostAction(input kcore.PostActionDefinition) *HTTPMockBuilder {
	b.mock.PostActionList = append(b.mock.PostActionList, input)

	return b
}

func (b *HTTPMockBuilder) Intercept(interceptor ...flow.Step) *HTTPMockBuilder {
	b.mock.Interceptors = append(b.mock.Interceptors, interceptor...)

	return b
}

// Delay sets a delay time before serving the mocked response.
func (b *HTTPMockBuilder) Delay(duration kcore.Delay) *HTTPMockBuilder {
	b.mock.Delay = duration

	return b
}

// Map adds a Mapper that allows modifying the response after it was built.
// Multiple mappers can be added.
func (b *HTTPMockBuilder) Map(mapper Mapper) *HTTPMockBuilder {
	b.mock.Mappers = append(b.mock.Mappers, mapper)

	return b
}

// Reply defines a response mock to be served if this mock matches a request.
func (b *HTTPMockBuilder) Reply(rep Reply) *HTTPMockBuilder {
	b.mock.Reply = rep

	return b
}

// Enabled define if the Mock will be enabled or disabled.
// All mocks are enabled by default.
func (b *HTTPMockBuilder) Enabled(enabled bool) *HTTPMockBuilder {
	b.mock.Switch(enabled)

	return b
}

// SetSource sets the source of the Mock.
// This could be a filename or any relevant information about the source of the Mock.
// This is mostly used internally.
func (b *HTTPMockBuilder) SetSource(src string) *HTTPMockBuilder {
	b.mock.Source = src

	return b
}

// Build builds a Mock with previously configured parameters.
// Used internally by HTTPMockApp.
func (b *HTTPMockBuilder) Build(app *HTTPMockApp) (*HTTPMock, error) {
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
		b.appendExpectation(&HTTPExpectation{
			Target:  int(matchingScopeScenario),
			Matcher: mfeat.Scenario(app.scenarioStore, b.scenario, b.scenarioRequiredState, b.scenarioNewState),
		})
	}

	for i, def := range b.mock.PostActionList {
		postAction, ok := app.config.PostActions[def.Name]
		if !ok {
			return nil, fmt.Errorf("mock: post action %s at index %d is not registered", def.Name, i)
		}

		if postAction == nil {
			return nil, fmt.Errorf("mock: post action %s at index %d is nil", def.Name, i)
		}
	}

	return b.mock, nil
}

func (b *HTTPMockBuilder) appendExpectation(e *kcore.Eval[*HTTPValueSelectorInput]) {
	b.mock.expectations = append(b.mock.expectations, e)
}

// --
// Request Values Selectors
// --

func selectScheme(_ context.Context, r *HTTPValueSelectorInput) any      { return r.URL.Scheme }
func selectMethod(_ context.Context, r *HTTPValueSelectorInput) any      { return r.RawRequest.Method }
func selectURL(_ context.Context, r *HTTPValueSelectorInput) any         { return r.URL.String() }
func selectURLPath(_ context.Context, r *HTTPValueSelectorInput) any     { return r.URL.Path }
func selectURLFragment(_ context.Context, r *HTTPValueSelectorInput) any { return r.URL.Fragment }
func selectHeader(k string) kcore.Selector[*HTTPValueSelectorInput] {
	return func(_ context.Context, r *HTTPValueSelectorInput) any { return r.RawRequest.Header.Get(k) }
}
func selectQuery(k string) kcore.Selector[*HTTPValueSelectorInput] {
	return func(_ context.Context, r *HTTPValueSelectorInput) any { return r.Query.Get(k) }
}
func selectQueries(k string) kcore.Selector[*HTTPValueSelectorInput] {
	return func(_ context.Context, r *HTTPValueSelectorInput) any { return r.Query[k] }
}
func selectBody(_ context.Context, r *HTTPValueSelectorInput) any { return r.ParsedBody }
func selectFormField(k string) kcore.Selector[*HTTPValueSelectorInput] {
	return func(_ context.Context, r *HTTPValueSelectorInput) any { return r.RawRequest.Form.Get(k) }
}
func selectRawRequest(_ context.Context, r *HTTPValueSelectorInput) any { return r.RawRequest }
