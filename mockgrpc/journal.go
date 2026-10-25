package mockgrpc

import (
	"context"
	"slices"
	"sync"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
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
		_ = grpc.SetHeader(ctx, metadata.Pairs(vlatte.RequestIDHeader, c.entry.ID))
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

// matched sets that mock matched the call.
func (c *journalCall) matched(mock *Mock) {
	c.update(func() { c.entry.MockID = mock.ID() })
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

// describeEval names the part of a call e checks, such as "header x-team".
func describeEval[T any](e *vlatte.Eval[T]) string {
	switch scope(e.Target) {
	case _scopeMethod:
		return "method"
	case _scopeHeader:
		return "header " + e.Key
	}

	return "field " + e.Key
}
