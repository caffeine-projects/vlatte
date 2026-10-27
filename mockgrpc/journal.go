package mockgrpc

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

var _ grpc.ServerStream = (*callStream)(nil)

// callKey is the key of the journal entry of a call in its context.
type callKey struct{}

// scope is the part of a call an evaluator checks, its Target: Key names a header, or a field.
type scope int

const (
	_scopeMethod scope = iota + 1
	_scopeHeader
	_scopeField
	_scopeRequest
	_scopeRepeat
	_scopeScenario
)

// openJournal opens the journal conf configures, nil when it configures none.
func (app *Server) openJournal(conf *Config) (*journal.Journal, error) {
	if conf.Journal == nil {
		return nil, nil
	}

	return journal.New(journal.Options{
		Dir:         conf.Journal.Dir,
		MaxEntries:  conf.Journal.MaxEntries,
		MaxBodySize: conf.Journal.MaxBodySize,
		Logger:      app.logger,
	})
}

// journaledService returns a copy of desc whose unary methods journal each call once it ends. grpc runs every unary
// interceptor within a method's handler, whether ServerOptions set it with grpc.UnaryInterceptor or chained it, so the
// entry has the code the client gets, and is appended before grpc sends it. desc stays as it is: generated code
// shares it.
func (app *Server) journaledService(desc *grpc.ServiceDesc) *grpc.ServiceDesc {
	d := *desc
	d.Methods = slices.Clone(desc.Methods)

	for i := range d.Methods {
		method, handler := "/"+desc.ServiceName+"/"+d.Methods[i].MethodName, d.Methods[i].Handler

		d.Methods[i].Handler = func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			call := app.newCall(ctx, method)

			res, err := handler(srv, context.WithValue(ctx, callKey{}, call), dec, interceptor)
			call.end(ctx, err)

			return res, err
		}
	}

	return &d
}

// journalUnary journals a unary call of a service registered on GRPCServer once it ends, from within the
// interceptors of ServerOptions; a call the handler journals already passes. It runs outside the recovery
// interceptor: a call that panicked ends as the failure the recovery makes of it.
func (in *interceptors) journalUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	if callOf(ctx) != nil {
		return next(ctx, req)
	}

	call := in.app.newCall(ctx, info.FullMethod)

	res, err := next(context.WithValue(ctx, callKey{}, call), req)
	call.end(ctx, err)

	return res, err
}

// journalStream journals a streaming call once it ends. grpc runs it before every other stream interceptor, so the
// entry has the code the client gets.
func (in *interceptors) journalStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
	call := in.app.newCall(ss.Context(), info.FullMethod)

	err := next(srv, &callStream{ServerStream: ss, ctx: context.WithValue(ss.Context(), callKey{}, call)})
	call.end(ss.Context(), err)

	return err
}

// callStream is a server stream whose context holds the journal entry of its call.
type callStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s *callStream) Context() context.Context {
	return s.ctx
}

// journalCall is the entry of a call in the server's journal, which the mock interceptors fill in, and the journal's
// method handler or interceptor appends once the call ends. An interceptor of ServerOptions may run the mock apart
// and answer first, so the mock may go on as the entry is appended: what it sets then is left out.
type journalCall struct {
	journal *journal.Journal

	// headers is set when the answer names its entry in a header.
	headers bool

	mu    sync.Mutex
	entry journal.Entry

	// served is set once a mock interceptor took the call: a call that an interceptor of ServerOptions answers itself
	// never is, and isn't the journal's.
	served bool
}

// newCall starts the entry of a call of ctx, of method.
func (app *Server) newCall(ctx context.Context, method string) *journalCall {
	md, _ := metadata.FromIncomingContext(ctx)

	return &journalCall{
		journal: app.Journal(),
		headers: !app.config.NoDebugHeaders,
		entry: journal.Entry{
			ID:     uuid.New().String(),
			Time:   time.Now(),
			Kind:   Kind,
			Path:   method,
			Target: method,
			Header: md.Copy(),
		},
	}
}

// serve sets that a mock interceptor took the call, of ctx: its answer has its request id in a header, as HTTP's debug
// headers have it, unless the server sends none.
func (c *journalCall) serve(ctx context.Context) {
	if c == nil {
		return
	}

	c.update(func() { c.served = true })

	if c.headers {
		_ = grpc.SetHeader(ctx, metadata.Pairs(vlatte.HeaderRequestID, c.entry.ID))
	}
}

// callOf returns the journal entry of the call of ctx, nil when the server keeps no journal: its methods do nothing.
func callOf(ctx context.Context) *journalCall {
	call, _ := ctx.Value(callKey{}).(*journalCall)
	return call
}

// update runs set on the call, unless the server keeps no journal.
func (c *journalCall) update(set func()) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	set()
}

// request sets the request message of the call, as JSON, which the call no longer changes.
func (c *journalCall) request(body []byte) {
	c.update(func() { c.entry.Body = body })
}

// matched sets that mock matched the call, and moved the scenarios of moves.
func (c *journalCall) matched(mock *Mock, moves []matchscenario.Move) {
	c.update(func() { c.entry.MockID, c.entry.Scenarios = mock.ID(), moves })
}

// unmatched sets that no mock matched the call: misses came closest.
func (c *journalCall) unmatched(misses []vlatte.NearMiss) {
	c.update(func() { c.entry.Outcome, c.entry.NearMisses = journal.OutcomeUnmatched, misses })
}

// replied sets that the mock that matched the call answered it, unless err, its reply's error, has no gRPC status: a
// mock's status is its answer, any other error a failure. A reply that failed as the client left, of ctx, is left to
// end, which tells the call was abandoned.
func (c *journalCall) replied(ctx context.Context, err error) {
	if err != nil && ctx.Err() != nil {
		return
	}

	c.update(func() {
		c.entry.Outcome = journal.OutcomeMatched

		if _, ok := status.FromError(err); !ok {
			c.entry.Outcome, c.entry.Message = journal.OutcomeFailed, err.Error()
		}
	})
}

// proxied sets that the target of a recording answered the call, as replied does for a mock's answer.
func (c *journalCall) proxied(ctx context.Context, err error) {
	c.replied(ctx, err)
	c.update(func() {
		if c.entry.Outcome == journal.OutcomeMatched {
			c.entry.Outcome = journal.OutcomeProxied
		}
	})
}

// failed sets that the call failed with err, whatever its answer.
func (c *journalCall) failed(err error) {
	c.update(func() { c.entry.Outcome, c.entry.Message = journal.OutcomeFailed, err.Error() })
}

// end appends the entry of the call, which ended with err, with its gRPC code. A call that reached no outcome, such
// as one that failed before matching, or panicked, failed, or was abandoned when its client left. A call that an
// interceptor answered itself, which reached no mock, isn't appended.
func (c *journalCall) end(ctx context.Context, err error) {
	c.mu.Lock()
	served, entry := c.served, c.entry
	c.mu.Unlock()

	if !served {
		return
	}

	entry.Status = int(status.Code(err))
	entry.Elapsed = time.Since(entry.Time)

	if entry.Outcome == journal.OutcomeUnknown {
		entry.Outcome = journal.OutcomeFailed
		if ctx.Err() != nil {
			entry.Outcome = journal.OutcomeAbandoned
		}

		if err != nil {
			entry.Message = err.Error()
		}
	}

	// The journal logs what it fails to keep.
	c.journal.Append(&entry)
}

// describeEval names the part of a call e checks, such as "header x-team". A scenario says the state the mock needs and
// the one it is in.
func describeEval[T any](e *vlatte.Eval[T]) string {
	switch scope(e.Target) {
	case _scopeMethod:
		return "method"
	case _scopeHeader:
		return "header " + e.Key
	case _scopeRequest:
		return "request " + e.Key
	case _scopeRepeat:
		return "times"
	case _scopeScenario:
		if sm, ok := e.Matcher.(*matchscenario.Matcher); ok {
			return sm.String()
		}

		return "scenario " + e.Key
	}

	return "field " + e.Key
}

// RequestPattern returns the filter of journal entries that the request part of b matches, as it matches a call: b is
// a *Builder, such as Method("Greetings").Field("message", match.Equal("hi")), whose reply counts for nothing. Times,
// Once and scenarios match no call served before, so a pattern has none. It implements journal.RequestPatterns.
func (app *Server) RequestPattern(b vlatte.Builder[*Mock, *Server]) (journal.Filter, error) {
	builder, ok := b.(*Builder)

	switch {
	case !ok:
		return nil, fmt.Errorf("grpc: a request pattern is a *Builder, such as Method(\"Greetings\"), not %T", b)
	case builder.err != nil:
		return nil, fmt.Errorf("grpc: request pattern: %w", builder.err)
	case builder.times > 0:
		return nil, errors.New("grpc: a request pattern has no Times or Once: they count the calls a mock serves")
	case builder.scenario != "":
		return nil, errors.New("grpc: a request pattern has no scenario: its state is now's, not the call's")
	}

	_, evals, err := builder.request(app)
	if err != nil {
		return nil, fmt.Errorf("grpc: request pattern: %w", err)
	}

	// A pattern that reads the request can't tell whether a request the journal cut matched: the method and the
	// header go first, as a miss there is a miss.
	var rest vlatte.Evaluators[*Mock, *Call]

	for _, ev := range evals {
		if s := scope(ev.Target); s == _scopeMethod || s == _scopeHeader {
			rest = append(rest, ev)
		}
	}

	return func(ctx context.Context, e *journal.Entry) (bool, error) {
		if e.Kind != Kind {
			return false, nil
		}

		md, ok := app.resolver.method(e.Path)
		if !ok {
			return false, nil
		}

		if e.BodyTruncated {
			ok, err := rest.Eval(ctx, vlatte.NewExplanation(), entryCall(app, e, md, nil))
			switch {
			case err != nil || !ok:
				return false, err
			case !builder.readsRequest:
				return true, nil
			}

			return false, fmt.Errorf("grpc: %w: entry %d: the journal kept part of its request, which the pattern reads",
				vlatte.ErrMatching, e.Seq)
		}

		call, err := decodeEntry(app, e, md)
		if err != nil {
			return false, fmt.Errorf("grpc: %w: entry %d: %w", vlatte.ErrMatching, e.Seq, err)
		}

		ok, err = vlatte.Evaluators[*Mock, *Call](evals).Eval(ctx, vlatte.NewExplanation(), call)
		if err != nil {
			return false, fmt.Errorf("grpc: %w: entry %d: %w", vlatte.ErrMatching, e.Seq, err)
		}

		return ok, nil
	}, nil
}

// entryCall returns the call of e, an entry of the server's journal, of the method md describes, with msgs, its
// request messages.
func entryCall(app *Server, e *journal.Entry, md protoreflect.MethodDescriptor, msgs []proto.Message) *Call {
	call := newCall(app, e.Path, md, metadata.MD(e.Header), nil)
	call.json = e.Body

	if md.IsStreamingClient() {
		call.messages = msgs
	} else if len(msgs) > 0 {
		call.message = msgs[0]
	}

	return call
}

// decodeEntry returns the call of e, whose request decodes from its body: a message, or an array of them for a
// streaming client.
func decodeEntry(app *Server, e *journal.Entry, md protoreflect.MethodDescriptor) (*Call, error) {
	if !md.IsStreamingClient() {
		msg, err := app.resolver.decode(md.Input(), e.Body)
		if err != nil {
			return nil, err
		}

		return entryCall(app, e, md, []proto.Message{msg}), nil
	}

	var raw []jsontext.Value
	if err := json.Unmarshal(e.Body, &raw); err != nil {
		return nil, fmt.Errorf("reading the messages: %w", err)
	}

	msgs := make([]proto.Message, len(raw))
	for i, r := range raw {
		msg, err := app.resolver.decode(md.Input(), r)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}

		msgs[i] = msg
	}

	return entryCall(app, e, md, msgs), nil
}
