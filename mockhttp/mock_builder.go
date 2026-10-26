package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
	"github.com/caffeine-projects/vlatte/internal/mockid"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*Builder)(nil)

var (
	ErrNoExpectations = errors.New("mock: at least 1 request matcher must be set")
	ErrNoReplies      = errors.New("mock: no reply set. Use .Reply() or any equivalent to set the expected mock response")
)

// errMockID says what an ID may hold. IDs name mocks in admin API paths, such as /mocks/{id}.
var errMockID = mockid.ErrInvalid

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
	pathParams            []namedMatcher // set with PathParam; Build makes them matchers, once it knows the template
	err                   error          // the first error of a method that can't return one, such as FormFile's or ID's
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

// Host defines a matcher for the host the request names, without its port, whatever the port: its Host header, or the
// host of a request in absolute form, as a forward proxy gets, such as "api.local". An IPv6 address comes without
// brackets. Host names ignore case: compare them with [match.EqualIgnoreCase].
func (b *Builder) Host(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeHost),
		Selector: selectHost,
		Matcher:  m,
	})

	return b
}

// ClientIP defines a matcher for the address of the client, as http.Request.RemoteAddr has it, without its port.
// Behind a proxy it is the proxy's: X-Forwarded-For is a header, which Header matches.
func (b *Builder) ClientIP(m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeClientIP),
		Selector: selectClientIP,
		Matcher:  m,
	})

	return b
}

// Method sets the HTTP request methods to match, ignoring case: one, or several, of which the request has one. It
// panics without a method.
func (b *Builder) Method(methods ...string) *Builder {
	var m vlatte.Matcher
	var key string

	if len(methods) == 0 {
		panic(".Method() requires at least one HTTP Method")
	} else if len(methods) == 1 {
		m = match.EqualIgnoreCase(methods[0])
		key = methods[0]
	} else {
		matchers := make([]vlatte.Matcher, len(methods))
		for i, method := range methods {
			matchers[i] = match.EqualIgnoreCase(method)
		}

		m = match.Any(matchers...)
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

// URLf matches the full URL, such as "http://127.0.0.1:8080/items?page=2", with a glob, formatted with the format
// specifier: only "*" is special, it crosses "/", and case counts.
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

// URLPathf matches the path with a glob, formatted with the format specifier: only "*" is special, it crosses "/",
// and case counts, so "/users/*" matches "/users/7/orders", and not "/Users/7".
func (b *Builder) URLPathf(format string, a ...any) *Builder {
	return b.URLPath(match.GlobMatch(fmt.Sprintf(format, a...)))
}

// RequestURI matches the path and query of the request exactly, as match.RequestURI does and a mock file's relative
// url, such as "/items?page=2", does: the same path, and the same parameters, in any order, each with its values in
// order. It panics when uri isn't a URL.
func (b *Builder) RequestURI(uri string) *Builder {
	return b.URL(match.RequestURI(uri))
}

// URLPathTemplate matches the path with a template, such as /users/{id}/orders/{orderId}: the path has as many
// segments, each literal one the same once decoded, and a non-empty one for each parameter. A parameter is a whole
// segment, and %7B writes a literal brace. PathParam matches a parameter, and templates read them as
// .Request.PathParams. A mock has one path template, which other matchers of the URL or the path add to.
func (b *Builder) URLPathTemplate(template string) *Builder {
	t, err := parsePathTemplate(template)
	if err == nil && b.spec.pathTemplate != nil {
		err = fmt.Errorf("the mock has path template %q already", b.spec.pathTemplate.text)
	}

	if err != nil {
		if b.err == nil {
			b.err = fmt.Errorf("path template %q: %w", template, err)
		}

		return b
	}

	b.spec.pathTemplate = t
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeURLPathTemplate),
		Key:      template,
		Selector: selectEscapedPath,
		Matcher:  t,
	})

	return b
}

// PathParam defines a matcher for a parameter of the path template, by its name: the segment it takes, decoded. With
// URLPathTemplate("/users/{id}"), PathParam("id", match.Equal("7")) matches /users/7. Build fails when the mock has
// no path template, or its template has no such parameter.
func (b *Builder) PathParam(name string, m vlatte.Matcher) *Builder {
	b.pathParams = append(b.pathParams, namedMatcher{name: name, matcher: m})

	return b
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

// Headers defines a matcher for every value of a header: a []string in the order the request sent them, empty when
// the request has no such header. Header reads only the first value. Values a request sends in one line, such as
// "a, b", are one value.
func (b *Builder) Headers(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeHeaders),
		Key:      name,
		Selector: selectHeaders(name),
		Matcher:  m,
	})

	return b
}

// Cookie defines a matcher for the value of a cookie, by its name, which keeps its case. It reads the first cookie
// with that name, as http.Request.Cookie does, and nil when the request has none, which no text matches.
func (b *Builder) Cookie(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeCookie),
		Key:      name,
		Selector: selectCookie(name),
		Matcher:  m,
	})

	return b
}

// BasicAuth matches the credentials of the request's Basic Authorization header, exactly.
func (b *Builder) BasicAuth(username, password string) *Builder {
	return b.BasicAuthMatches(match.StrictEqual(username), match.StrictEqual(password))
}

// BasicAuthMatches defines matchers for the username and the password of the request's Basic Authorization header,
// as http.Request.BasicAuth reads them. Without Basic credentials, both are nil.
func (b *Builder) BasicAuthMatches(username, password vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeBasicAuth),
		Key:      "username",
		Selector: selectBasicAuthUsername,
		Matcher:  username,
	})
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeBasicAuth),
		Key:      "password",
		Selector: selectBasicAuthPassword,
		Matcher:  password,
	})

	return b
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

// FormFields defines a matcher for every value of a field of the body's form: a []string, empty when the form has no
// such field. Like FormField, it never reads the query.
func (b *Builder) FormFields(name string, m vlatte.Matcher) *Builder {
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeFormFields),
		Key:      name,
		Selector: selectFormFields(name),
		Matcher:  m,
	})

	return b
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
		Key:      "request",
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

// ScenarioIs puts the mock in the given scenario. Without [Builder.ScenarioStateIs], it matches in any state of it.
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
// It starts the scenario when no mock did yet.
func (b *Builder) ScenarioStateWillBe(newState string) *Builder {
	b.scenarioNewState = newState

	return b
}

// Callback adds fn as an on_complete event of the mock, named callback in the logs, with no listener to register. It
// runs as the mock's other events and the global ones do: at the same time as them, once the mock replied, and the
// response waits for it, so a test checks what it did right after the call. fn gets an [*OnComplete], read-only as
// the events run at the same time, and a context that the client leaving doesn't end, and [Server.Close] does. An
// error or a panic of fn is logged as a warning, unless Close canceled it, and never changes the response. Mock files
// can't set callbacks, and a mock's description leaves them out.
func (b *Builder) Callback(fn vlatte.EventFunc[vlatte.OnComplete]) *Builder {
	b.spec.events = append(b.spec.events, vlatte.EventConfig{Name: "callback", Listener: fn})
	return b
}

// OnCompleteEvent adds an on-complete event to the mock. It runs once the mock replied, and the response waits for
// it.
func (b *Builder) OnCompleteEvent(cfg vlatte.EventConfig) *Builder {
	b.spec.events = append(b.spec.events, cfg)
	return b
}

// Step adds steps that the body of each answer of the mock passes through, in order, before it reaches the client:
// to delay it, send it in parts, change it or cut it. See [vlatte.Step]. Each answer has a flow of its own, so a
// stage keeps what it counts for that answer only.
//
// Flushes pass through the steps too, and so does the end of the answer, an empty body included. A stage that
// aborts the flow, with [vlatte.ErrAbort] or [vlatte.ErrReset], cuts the answer after what it sent: the mock counts
// the call and runs its callbacks and events, then cuts the connection. ErrReset resets an HTTP/1 connection.
// A failure, such as a failing reply, is vlatte's answer, and skips the steps.
func (b *Builder) Step(steps ...vlatte.Step) *Builder {
	b.spec.steps = append(b.spec.steps, steps...)

	return b
}

// WireStep adds steps that every byte of each answer of the mock passes through, in order, as the client reads it:
// the status line, the headers, the framing of the body and the body, after the steps of Step. They can send what
// isn't HTTP: nothing at all, half the headers, garbage after a valid head. See [vlatte.Step].
//
// The mock answers on its connection, which it takes over, in HTTP/1.x, with Connection: close, so the connection
// ends with the answer. A request over HTTP/2 can't give its connection over: it gets vlatte's error answer. A stage
// that aborts the flow, with [vlatte.ErrAbort] or [vlatte.ErrReset], has the connection closed, or reset, without
// the rest of the answer, once the mock counted the call and ran its callbacks and events. A failure, such as a failing reply, is vlatte's
// answer, and skips the steps. A recording leaves the call out.
func (b *Builder) WireStep(steps ...vlatte.Step) *Builder {
	b.spec.wireSteps = append(b.spec.wireSteps, steps...)

	return b
}

// UpstreamStep adds steps that what the mock sends on passes through, in order, as its target reads it: the body of
// the request a proxy reply sends, such as to delay it, send it in parts or cut it. See [vlatte.Step]. Steps can
// change its length, so the target gets it chunked. A request without a body has nothing for them.
//
// The server reads the body of a request before it matches a mock, so the client's upload has ended by then: these
// steps apply to what the mock sends on, not to how it reads the client. A body the server doesn't read whole, with
// NoBodyParsing or past MaxBodyParsingLimit, is read from the client as the target reads it, a small piece at a time,
// so a stage that slows it slows the upload too, once the connection's buffers are full. A mock with wire steps
// takes its connection over before it replies, so it reads such a body through these steps first: they pace the
// upload, and a proxy's target gets the body as they passed it on, at once. A stage that aborts
// the flow, with [vlatte.ErrAbort] or [vlatte.ErrReset], fails the call to the target and cuts the client's answer,
// once the mock counted the call.
func (b *Builder) UpstreamStep(steps ...vlatte.Step) *Builder {
	b.spec.upSteps = append(b.spec.upSteps, steps...)

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
// Multiple mappers can be added. A mapper finds the body in Body: one that streams, such as a proxy's, is read whole
// for it first.
func (b *Builder) Map(mapper Mapper) *Builder {
	b.spec.mappers = append(b.spec.mappers, mapper)

	return b
}

// Reply sets what the mock replies with. Build asks f for a new Replier for each mock, so f can serve many mocks.
// Reply, SequentialReply, RandomReply, ProxyReply, FuncReply and EchoReply are factories, and Shared turns any Replier
// into one. The mock owns the Replier f makes: when it implements io.Closer, the mock closes it when it leaves the
// server. A Replier that Shared gives to many mocks is never closed by them.
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

	params, err := b.pathParamExpectations()
	if err != nil {
		return nil, fmt.Errorf("mock: %w", err)
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

	spec.expectations = append(spec.expectations, params...)

	// The matchers that keep state for one mock are made for it: Times counts the requests of this mock only, and the
	// scenario runs in the store of app.
	if b.times > 0 {
		spec.expectations = append(spec.expectations, &expectation{
			Target:  int(matchingScopeRepeat),
			Key:     "times",
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
		if def.Listener != nil {
			continue
		}

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

	return &Mock{spec: spec, reply: reply, replyDesc: desc, closer: owned(b.reply, reply), enabled: b.enabled}, nil
}

// pathParamExpectations returns the matchers of the path parameters. A path parameter reads the segment its template
// names, so it needs the template, whichever was set first.
func (b *Builder) pathParamExpectations() ([]*expectation, error) {
	t := b.spec.pathTemplate
	params := make([]*expectation, 0, len(b.pathParams))
	for _, p := range b.pathParams {
		switch {
		case t == nil:
			return nil, fmt.Errorf("path param %q: the mock has no path template: set one with URLPathTemplate", p.name)
		case !t.has(p.name):
			return nil, fmt.Errorf("path param %q: path template %q has no such parameter", p.name, t.text)
		}

		params = append(params, &expectation{
			Target:   int(matchingScopePathParam),
			Key:      p.name,
			Selector: selectPathParam(t, p.name),
			Matcher:  p.matcher,
		})
	}

	return params, nil
}

func (b *Builder) appendExpectation(e *vlatte.Eval[*selectorInput]) {
	b.spec.expectations = append(b.spec.expectations, e)
}

// checkMockID accepts the IDs a mock can have: see mockid.Check.
func checkMockID(id string) error {
	return mockid.Check(id)
}

// --
// Request Values Selectors
// --

func selectScheme(_ context.Context, r *selectorInput) any { return r.URL.Scheme }

func selectMethod(_ context.Context, r *selectorInput) any      { return r.RawRequest.Method }
func selectURL(_ context.Context, r *selectorInput) any         { return r.URL.String() }
func selectURLPath(_ context.Context, r *selectorInput) any     { return r.URL.Path }
func selectURLFragment(_ context.Context, r *selectorInput) any { return r.URL.Fragment }
func selectEscapedPath(_ context.Context, r *selectorInput) any { return r.URL.EscapedPath() }

// selectPathParam selects the parameter name of t from the path. Its matcher runs after t's, so the path fits t.
func selectPathParam(t *pathTemplate, name string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any {
		params, _ := t.params(r.URL.EscapedPath())
		return params[name]
	}
}

func selectHeader(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.RawRequest.Header.Get(k) }
}

func selectHeaders(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.RawRequest.Header.Values(k) }
}

// selectCookie selects the value of the first cookie named name, or nil without one, as selectBasicAuthUsername does.
func selectCookie(name string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any {
		c, err := r.RawRequest.Cookie(name)
		if err != nil {
			return nil
		}

		return c.Value
	}
}

// selectBasicAuthUsername selects the username of Basic credentials, or nil without them, so that no text matches a
// request without credentials.
func selectBasicAuthUsername(_ context.Context, r *selectorInput) any {
	username, _, ok := r.RawRequest.BasicAuth()
	if !ok {
		return nil
	}

	return username
}

// selectBasicAuthPassword selects the password of Basic credentials, or nil without them, as selectBasicAuthUsername.
func selectBasicAuthPassword(_ context.Context, r *selectorInput) any {
	_, password, ok := r.RawRequest.BasicAuth()
	if !ok {
		return nil
	}

	return password
}

func selectHost(_ context.Context, r *selectorInput) any { return hostOnly(r.RawRequest.Host) }

// selectClientIP selects the address of the client, which a middleware may have set without a port.
func selectClientIP(_ context.Context, r *selectorInput) any {
	return hostOnly(r.RawRequest.RemoteAddr)
}

// hostOnly returns an address without its port and the brackets of an IPv6 address: "api.local:8080" and
// "api.local:x" are "api.local", and "[::1]:80", "[::1]" and "::1" are "::1". Unlike url.URL.Hostname, it takes any
// port, as net/http does, and an IPv6 address without brackets.
func hostOnly(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}

	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
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

// selectFormFields selects every value of a field of a form body, as selectFormField does its first.
func selectFormFields(k string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.Form[k] }
}
func selectRawRequest(_ context.Context, r *selectorInput) any { return r.RawRequest }
