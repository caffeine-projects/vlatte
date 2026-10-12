package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*Builder)(nil)

var (
	ErrNoExpectations = errors.New("mock: at least 1 request matcher must be set")
	ErrNoReplies      = errors.New("mock: no reply set. Use .Reply() or any equivalent to set the expected mock response")
)

// errMockID says what an ID may hold. IDs name mocks in admin API paths, such as /mocks/{id}.
var errMockID = errors.New("an id holds only letters, digits, '.', '_', '~' and '-'")

// Builder describes a mock. Build makes a new Mock from it each time, for the given server, and leaves the builder as
// it was: one builder can be added to many servers, or more than once to one, and changing it after Mock changes only
// the mocks built afterwards.
//
// Mocks built from one builder share what it holds: its matchers, mappers, callbacks, steps and delay. Each has its
// own ID (unless set with ID), hits, count of Times and Once, enabled state, scenario binding and Replier.
type Builder struct {
	spec                  mockSpec
	reply                 ReplierFactory
	enabled               bool
	times                 int64 // the requests each mock built serves, set with Times; 0 is no limit
	scenario              string
	scenarioNewState      string
	scenarioRequiredState string
	err                   error // the first error of a method that can't return one, such as FormFile's or ID's
}

// Request creates a new empty Builder.
func Request() *Builder {
	return &Builder{enabled: true}
}

// AnyMethod creates a new empty Builder.
func AnyMethod() *Builder {
	return Request().MethodMatches(match.Anything())
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

// ID sets the ID of the mock, such as "users-get". Without one, each mock built gets a generated ID.
// An ID holds only letters, digits, '.', '_', '~' and '-', and a server holds one mock per ID.
func (b *Builder) ID(id string) *Builder {
	if err := checkMockID(id); err != nil && b.err == nil {
		b.err = fmt.Errorf("id %q: %w", id, err)
	}

	b.spec.id = id

	return b
}

// Name defines a name for the mock.
// Useful for debugging.
func (b *Builder) Name(name string) *Builder {
	b.spec.name = name

	return b
}

// Priority sets the priority of the mock. Mocks with a lower priority are matched first;
// mocks of equal priority, in the order they were added.
func (b *Builder) Priority(p int) *Builder {
	b.spec.priority = p

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
		Target:   int(matchingScopeURLPath),
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

// URLFragment defines a matcher to be applied to the URL fragment: the decoded text after a "#" in the request
// target. An escaped "%23" is part of the path. Go's http.Client doesn't send fragments: a request with URL.Opaque
// set to the path and fragment, such as "/users#top", does.
func (b *Builder) URLFragment(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeURLFragment),
		Selector: selectURLFragment,
		Matcher:  m,
	})

	return b
}

// URLFragmentf sets a Matcher that compares the URL fragment with the given value, as a glob.
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

// ContentType matches the Content-Type of the request, ignoring case. A value without parameters matches the media
// type alone, whatever parameters the request has, such as the boundary of a multipart form:
// ContentType("multipart/form-data") matches "multipart/form-data; boundary=x". A value with parameters matches the
// whole header.
func (b *Builder) ContentType(value string, a ...any) *Builder {
	v := fmt.Sprintf(value, a...)
	if strings.Contains(v, ";") {
		return b.Header(HeaderContentType, match.EQI(v))
	}

	return b.Header(HeaderContentType, match.Split(";", match.Item(0, match.Trim(match.EQI(v)))))
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

// FormField defines a matcher for a field of the body's form, urlencoded or multipart, by its name.
// It reads the field as http.Request.PostFormValue does: its first value, and never the query.
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

// Times sets how many requests each mock built from the builder serves: 1 or more. Each mock counts its own, so a mock
// built again, as Remock does, starts from zero. The last call wins.
func (b *Builder) Times(times int64) *Builder {
	if times < 1 && b.err == nil {
		b.err = fmt.Errorf("times %d: expected 1 or more", times)
	}

	b.times = times

	return b
}

// Once sets that each mock built from the builder serves one request.
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
	b.spec.callbacks = append(b.spec.callbacks, callback)

	return b
}

// OnCompleteEvent adds an on-complete event to the mock.
func (b *Builder) OnCompleteEvent(cfg vlatte.EventConfig) *Builder {
	b.spec.events = append(b.spec.events, cfg)
	return b
}

func (b *Builder) Step(steps ...vlatte.Step) *Builder {
	b.spec.steps = append(b.spec.steps, steps...)

	return b
}

// Delay waits for the given duration before serving the mocked response.
func (b *Builder) Delay(duration time.Duration) *Builder {
	b.spec.delay = vlatte.Latency(duration)

	return b
}

// DelayFunc sets a function that picks the delay before each mocked response,
// such as [vlatte.Jitter] or [vlatte.UniformDistribution].
func (b *Builder) DelayFunc(delay vlatte.Delay) *Builder {
	b.spec.delay = delay

	return b
}

// Map adds a Mapper that allows modifying the response after it was built.
// Multiple mappers can be added.
func (b *Builder) Map(mapper Mapper) *Builder {
	b.spec.mappers = append(b.spec.mappers, mapper)

	return b
}

// Reply sets what the mock replies with. Build asks f for a new Replier for each mock, so f can serve many mocks.
// Reply, SequentialReply, RandomReply, ProxyReply, FuncReply and EchoReply are factories, and Shared turns any Replier
// into one.
func (b *Builder) Reply(f ReplierFactory) *Builder {
	b.reply = f
	return b
}

// Enabled sets whether the mocks built afterwards start enabled. All mocks are enabled by default.
// A mock already built changes through its Scope.
func (b *Builder) Enabled(enabled bool) *Builder {
	b.enabled = enabled

	return b
}

// SetSource sets the source of the Mock.
// This could be a filename or any relevant information about the source of the Mock.
// This is mostly used internally.
func (b *Builder) SetSource(src string) *Builder {
	b.spec.origin = src

	return b
}

// Build makes a new Mock for app from the builder, and leaves the builder as it was.
// Used internally by Server.
func (b *Builder) Build(app *Server) (*Mock, error) {
	if b.err != nil {
		return nil, fmt.Errorf("mock: %w", b.err)
	}

	// Times counts here as it did when it was a matcher of the builder: a mock limited only by Times, such as a mock
	// file with repeat and no request keys, still builds.
	if len(b.spec.expectations) == 0 && b.times == 0 {
		return nil, ErrNoExpectations
	}

	if b.reply == nil {
		return nil, ErrNoReplies
	}

	spec := b.spec.clone()
	if spec.id == "" {
		spec.id = uuid.New().String()
	}

	// The matchers that keep state for one mock are made for it: Times counts the requests of this mock only, and the
	// scenario runs in the store of app.
	if b.times > 0 {
		spec.expectations = append(spec.expectations, &expectation{
			Target:  int(matchingScopeNone),
			Matcher: mfeat.Repeat(b.times),
		})
	}

	if b.scenario != "" {
		spec.expectations = append(spec.expectations, &expectation{
			Target:  int(matchingScopeScenario),
			Matcher: mfeat.Scenario(app.scenarioStore, b.scenario, b.scenarioRequiredState, b.scenarioNewState),
		})
	}

	// As prepareGlobalEvents does: the prepared parameters go into the mock's copy, never into the builder.
	for i, def := range spec.events {
		if app.config.Listeners == nil || !app.config.Listeners.OnComplete.Has(def.Name) {
			return nil, fmt.Errorf("mock: on_complete listener %s at index %d is not registered", def.Name, i)
		}

		params, err := app.config.Listeners.OnComplete.Prepare(def, app.config.TemplateEngine)
		if err != nil {
			return nil, fmt.Errorf("mock: on_complete listener %s at index %d: %w", def.Name, i, err)
		}

		spec.events[i].Parameters = params
	}

	// Last, so a failure above makes nothing for the mock, such as the HTTP client of a proxy.
	reply, err := newReplier(b.reply, app)
	if err != nil {
		return nil, err
	}

	desc, _ := descriptor.IsSelfDescriptive(b.reply)

	return &Mock{spec: spec, reply: reply, replyDesc: desc, enabled: b.enabled}, nil
}

func (b *Builder) appendExpectation(e *vlatte.Eval[*selectorInput]) {
	b.spec.expectations = append(b.spec.expectations, e)
}

// checkMockID accepts the IDs a mock can have: letters, digits, '.', '_', '~' and '-', the characters
// a URL path needs no escaping for.
func checkMockID(id string) error {
	if id == "" || strings.ContainsFunc(id, notIDRune) {
		return errMockID
	}

	return nil
}

// notIDRune reports whether r can't be part of a mock ID.
func notIDRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return false
	default:
		return !strings.ContainsRune("._~-", r)
	}
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

// selectFormField selects a field of a form body. Only the body counts: http.Request.Form also holds the query.
func selectFormField(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.Form.Get(k) }
}
func selectRawRequest(_ context.Context, r *selectorInput) any { return r.RawRequest }
