package mockgrpc

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
	"github.com/caffeine-projects/vlatte/internal/mockid"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.Builder[*Mock, *Server] = (*Builder)(nil)

// errBidiMatcher is the error of a bidi streaming mock with a matcher of the request: it matches as the call starts,
// before the client sent anything.
var errBidiMatcher = errors.New("a bidi streaming mock matches the method and the header only, as the call starts: " +
	"match each message the client sends with Bidi().On")

// Builder describes a mock of a gRPC method. Build makes a new Mock from it each time, for the given server, and
// leaves the builder as it was: one builder can be added to many servers, or more than once to one.
//
// Mocks built from one builder share its matchers and delay. Each has its own ID (unless set with ID), hits, count of
// Times and Once, enabled state, scenario binding and Replier.
type Builder struct {
	method                string
	id                    string
	name                  string
	priority              int
	enabled               bool
	times                 int64 // the calls each mock built serves, set with Times; 0 is no limit
	scenario              string
	scenarioRequiredState string
	scenarioNewState      string
	delay                 vlatte.Delay
	events                vlatte.EventConfigs
	expectations          []*vlatte.Eval[*Call]
	readsRequest          bool // a matcher reads the request, which a bidi call hasn't sent when it matches
	reply                 Reply
	origin                string
	err                   error // the first error of a method that can't return one, such as ID's
}

// Method starts a mock of a method, named by whole segments of its full method "/pkg.Service/Method": "Method",
// "Service/Method", "pkg.Service/Method" or the full method, such as a generated pb.Service_Method_FullMethodName.
// A part of a name, such as "Meth", names none.
//
// The mock is built for the methods its name names: those of the services whose generated code the program imports,
// or whose descriptors the server was given with [ConfigBuilder.Descriptors] or [ConfigBuilder.Files]. Build fails
// with [ErrUnknownMethod] when it names none, and with [ErrAmbiguousMethod] when it names methods of different call
// types. The call type of the method decides how the mock serves it: unary, server, client or bidi streaming.
func Method(name string) *Builder {
	return &Builder{method: name, enabled: true}
}

// ID sets the ID of the mock, such as "greet-hi". Without one, each mock built gets a generated ID.
// An ID holds only letters, digits, '.', '_', '~' and '-', and a server holds one mock per ID.
func (b *Builder) ID(id string) *Builder {
	if err := mockid.Check(id); err != nil && b.err == nil {
		b.err = fmt.Errorf("id %q: %w", id, err)
	}

	b.id = id

	return b
}

// Name sets a name for the mock, which debug headers, logs and assertion messages show.
func (b *Builder) Name(name string) *Builder {
	b.name = name
	return b
}

// Priority sets the priority of the mock. Mocks with a lower priority are matched first; mocks of equal priority, in
// the order they were added.
func (b *Builder) Priority(p int) *Builder {
	b.priority = p
	return b
}

// Enabled sets whether the mocks built afterwards start enabled. All mocks are enabled by default.
// A mock already built changes through its Scope.
func (b *Builder) Enabled(enabled bool) *Builder {
	b.enabled = enabled
	return b
}

// Header matches the metadata the client sent under key: its value, when it sent one, else all of them, nil when it
// sent none. gRPC metadata keys are lowercase.
func (b *Builder) Header(key string, m vlatte.Matcher) *Builder {
	b.expectations = append(b.expectations, &vlatte.Eval[*Call]{
		Target:   int(_scopeHeader),
		Key:      key,
		Matcher:  m,
		Selector: selectHeader(key),
	})

	return b
}

// Field matches the request at path, a JSON path, such as "user_id" or "$.items[0].key", with m. The request is read
// as protojson writes it, with the field names of the .proto file: enums as names, int64 values as text. A client
// streaming call is read as the array of its messages, so "$[0].key" reads its first message.
func (b *Builder) Field(path string, m vlatte.Matcher) *Builder {
	b.readsRequest = true
	b.expectations = append(b.expectations, &vlatte.Eval[*Call]{
		Target:   int(_scopeField),
		Key:      path,
		Matcher:  match.Field(path, m),
		Selector: selectJSON,
	})

	return b
}

// JSON matches a request whose JSON, as Field reads it, equals text, by match.EqualJSON's rule: fields the request
// leaves out are its zero values, which text leaves out too.
func (b *Builder) JSON(text string) *Builder {
	if !jsontext.Value(text).IsValid() && b.err == nil {
		b.err = fmt.Errorf("json: not JSON: %q", text)
		return b
	}

	b.readsRequest = true
	b.expectations = append(b.expectations, &vlatte.Eval[*Call]{
		Target:   int(_scopeRequest),
		Key:      "json",
		Matcher:  match.EqualJSON(text),
		Selector: selectJSON,
	})

	return b
}

// Message matches a request message equal to msg, by proto.Equal: the request of a unary or server streaming call.
func (b *Builder) Message(msg proto.Message) *Builder {
	return b.messages("message", []proto.Message{msg})
}

// Messages matches the messages of a client streaming call, in order, each equal to its own by proto.Equal.
func (b *Builder) Messages(msgs ...proto.Message) *Builder {
	return b.messages("messages", msgs)
}

// CallMatches matches a call fn reports true for. fn reads the call as matchers do, with its request: see [Call].
func (b *Builder) CallMatches(fn func(call *Call) (bool, error)) *Builder {
	b.readsRequest = true
	b.expectations = append(b.expectations, &vlatte.Eval[*Call]{
		Target: int(_scopeRequest),
		Key:    "call",
		Matcher: match.Function(func(v any) (bool, error) {
			call, ok := v.(*Call)
			if !ok {
				return false, fmt.Errorf("mock: call matcher got %T, want *Call", v)
			}

			return fn(call)
		}),
		Selector: selectCall,
	})

	return b
}

// Times sets how many calls each mock built from the builder serves: 1 or more. Each mock counts its own, so a mock
// built again, as Remock does, starts from zero. The last call wins.
func (b *Builder) Times(times int64) *Builder {
	if times < 1 && b.err == nil {
		b.err = fmt.Errorf("times %d: expected 1 or more", times)
	}

	b.times = times

	return b
}

// Once sets that each mock built from the builder serves one call.
func (b *Builder) Once() *Builder {
	return b.Times(1)
}

// StartScenario sets that this mock starts a new scenario with the given name: it matches while the scenario is in its
// first state, "started".
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

// ScenarioStateIs sets that the mock matches only while its scenario is in requiredState.
func (b *Builder) ScenarioStateIs(requiredState string) *Builder {
	b.scenarioRequiredState = requiredState
	return b
}

// ScenarioStateWillBe sets the state the scenario moves to once this mock answered a call. It starts the scenario when
// no mock did yet.
func (b *Builder) ScenarioStateWillBe(newState string) *Builder {
	b.scenarioNewState = newState
	return b
}

// Delay waits for d before the mock replies. A client that leaves during the delay gets nothing, and the mock doesn't
// count the call.
func (b *Builder) Delay(d time.Duration) *Builder {
	b.delay = vlatte.Latency(d)
	return b
}

// DelayFunc sets a function that picks the delay before each reply, such as [vlatte.Jitter] or
// [vlatte.UniformDistribution].
func (b *Builder) DelayFunc(delay vlatte.Delay) *Builder {
	b.delay = delay
	return b
}

// Callback adds fn as an on_complete event of the mock, named callback in the logs, with no listener to register. It
// runs as the mock's other events and the global ones do: at the same time as them, once the mock replied, and the
// call ends after it, so a test checks what it did right after the call. fn gets an [*OnComplete], read-only as the
// events run at the same time, and a context that the client leaving doesn't end, and [Server.Close] does. An error
// or a panic of fn is logged as a warning, unless Close canceled it, and never changes the answer. Mock files can't
// set callbacks, and a mock's description leaves them out.
func (b *Builder) Callback(fn vlatte.EventFunc[vlatte.OnComplete]) *Builder {
	b.events = append(b.events, vlatte.EventConfig{Name: "callback", Listener: fn})
	return b
}

// OnCompleteEvent adds an on_complete event to the mock, such as a webhook: it runs once the mock replied, and the
// call ends after it. Unless it carries its own Listener, its Name names a listener the server registered: see
// [ConfigBuilder.OnCompleteListener].
func (b *Builder) OnCompleteEvent(cfg vlatte.EventConfig) *Builder {
	b.events = append(b.events, cfg)
	return b
}

// Reply sets what the mock replies with: [Message], [Messages], [JSON], [Template], [Status], [Bidi] or [Func]. Build
// asks r for a new Replier for each mock, so r can serve many mocks.
func (b *Builder) Reply(r Reply) *Builder {
	b.reply = r
	return b
}

// SetSource sets where the mock comes from, such as the mock file that describes it, which logs and the admin API show.
func (b *Builder) SetSource(src string) *Builder {
	b.origin = src
	return b
}

// Build makes a new Mock for app from the builder, and leaves the builder as it was. Server.Mock calls it.
func (b *Builder) Build(app *Server) (*Mock, error) {
	if b.err != nil {
		return nil, fmt.Errorf("mock: %w", b.err)
	}

	if b.reply == nil {
		return nil, errNoReply
	}

	methods, expectations, err := b.request(app)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(methods))
	for i, md := range methods {
		names[i] = fullMethod(md)
	}

	// The matchers that keep state for one mock are made for it: Times counts the calls of this mock only, and the
	// scenario runs in the store of app.
	if b.times > 0 {
		expectations = append(expectations, &vlatte.Eval[*Call]{
			Target:  int(_scopeRepeat),
			Key:     "times",
			Matcher: mfeat.Repeat(b.times),
		})
	}

	if b.scenario != "" {
		expectations = append(expectations, &vlatte.Eval[*Call]{
			Target:  int(_scopeScenario),
			Key:     b.scenario,
			Matcher: mfeat.Scenario(app.scenarios, b.scenario, b.scenarioRequiredState, b.scenarioNewState),
		})
	}

	events, err := app.prepareEvents(b.events, "mock: on_complete listener")
	if err != nil {
		return nil, err
	}

	replier, err := newReplier(b.reply, app, methods)
	if err != nil {
		return nil, fmt.Errorf("mock: %s: %w", b.method, err)
	}

	id := b.id
	if id == "" {
		id = uuid.New().String()
	}

	return &Mock{
		id:           id,
		name:         b.name,
		priority:     b.priority,
		enabled:      b.enabled,
		origin:       b.origin,
		method:       b.method,
		methods:      names,
		callType:     typeOf(methods[0]),
		expectations: expectations,
		delay:        b.delay,
		events:       events,
		reply:        b.reply,
		replier:      replier,
	}, nil
}

// request returns the methods the builder names, and the matchers of its request: the method first, so a call of
// another method misses on it, and its near misses say so.
func (b *Builder) request(app *Server) ([]protoreflect.MethodDescriptor, []*vlatte.Eval[*Call], error) {
	methods, err := app.resolver.find(b.method)
	if err != nil {
		return nil, nil, err
	}

	if typeOf(methods[0]) == _bidiStream && b.readsRequest {
		return nil, nil, fmt.Errorf("mock: %s: %w", b.method, errBidiMatcher)
	}

	names := make([]string, len(methods))
	for i, md := range methods {
		names[i] = fullMethod(md)
	}

	expectations := make([]*vlatte.Eval[*Call], 0, len(b.expectations)+3)
	expectations = append(expectations, &vlatte.Eval[*Call]{
		Target:   int(_scopeMethod),
		Key:      b.method,
		Matcher:  methodIn(names),
		Selector: selectMethod,
	})

	return methods, append(expectations, b.expectations...), nil
}

// messages matches the messages of the request, the value selectMessages gives, with msgs. key names the matcher in
// near misses.
func (b *Builder) messages(key string, msgs []proto.Message) *Builder {
	b.readsRequest = true
	b.expectations = append(b.expectations, &vlatte.Eval[*Call]{
		Target:   int(_scopeRequest),
		Key:      key,
		Matcher:  protoEqual(msgs),
		Selector: selectMessages,
	})

	return b
}

// protoEqual matches a list of messages, the value selectMessages gives, each equal to its own of want by proto.Equal.
func protoEqual(want []proto.Message) vlatte.Matcher {
	return vlatte.MatcherFunc("protoEqual", func(arg vlatte.MatcherArg) (bool, error) {
		got, _ := arg.Value().([]proto.Message)

		return slices.EqualFunc(got, want, proto.Equal), nil
	})
}

func selectMethod(_ context.Context, c *Call) any {
	return c.method
}

func selectHeader(k string) vlatte.Selector[*Call] {
	return func(_ context.Context, c *Call) any {
		v := c.header.Get(k)

		switch len(v) {
		case 0:
			return nil
		case 1:
			return v[0]
		}

		return v
	}
}

func selectJSON(_ context.Context, c *Call) any {
	return string(c.json)
}

func selectMessages(_ context.Context, c *Call) any {
	return c.Messages()
}

func selectCall(_ context.Context, c *Call) any {
	return c
}
