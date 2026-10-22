package mockgrpc

import (
	"context"
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

// journalUnary journals a unary call once it ends. It runs outside the recovery interceptor: a call that panicked
// ends as the failure the recovery makes of it.
func (in *interceptors) journalUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	call := in.app.newCall(ctx, info.FullMethod)

	res, err := next(context.WithValue(ctx, callKey{}, call), req)
	call.end(ctx, err)

	return res, err
}

// journalStream journals a streaming call once it ends, as journalUnary does.
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

// journalCall is the entry of a call in the server's journal, which the mock interceptors fill in, and the journal
// interceptor appends once the call ends. A call runs on one goroutine.
type journalCall struct {
	journal *journal.Journal
	entry   journal.Entry
}

// newCall starts the entry of a call of method, its request id in the header of its answer, as HTTP's debug headers
// have it, unless the server sends none.
func (app *Server) newCall(ctx context.Context, method string) *journalCall {
	id := uuid.New().String()

	if !app.config.NoDebugHeaders {
		_ = grpc.SetHeader(ctx, metadata.Pairs(vlatte.RequestIDHeader, id))
	}

	md, _ := metadata.FromIncomingContext(ctx)

	return &journalCall{
		journal: app.Journal(),
		entry: journal.Entry{
			ID:     id,
			Time:   time.Now(),
			Kind:   Kind,
			Path:   method,
			Target: method,
			Header: md.Copy(),
		},
	}
}

// callOf returns the journal entry of the call of ctx, nil when the server keeps no journal: its methods do nothing.
func callOf(ctx context.Context) *journalCall {
	call, _ := ctx.Value(callKey{}).(*journalCall)
	return call
}

// request sets the request message of the call, as JSON, which the call no longer changes.
func (c *journalCall) request(body []byte) {
	if c != nil {
		c.entry.Body = body
	}
}

// matched sets that mock matched the call.
func (c *journalCall) matched(mock *Mock) {
	if c != nil {
		c.entry.MockID = mock.ID()
	}
}

// unmatched sets that no mock matched the call: misses came closest.
func (c *journalCall) unmatched(misses []vlatte.NearMiss) {
	if c != nil {
		c.entry.Outcome, c.entry.NearMisses = journal.OutcomeUnmatched, misses
	}
}

// replied sets that the mock that matched the call answered it, unless err, its reply's error, has no gRPC status: a
// mock's status is its answer, any other error a failure. A reply that failed as the client left, of ctx, is left to
// end, which tells the call was abandoned.
func (c *journalCall) replied(ctx context.Context, err error) {
	if c == nil || err != nil && ctx.Err() != nil {
		return
	}

	c.entry.Outcome = journal.OutcomeMatched

	if _, ok := status.FromError(err); !ok {
		c.entry.Outcome, c.entry.Message = journal.OutcomeFailed, err.Error()
	}
}

// end appends the entry of the call, which ended with err, with its gRPC code. A call that reached no outcome, such
// as one that failed before matching, or panicked, failed, or was abandoned when its client left.
func (c *journalCall) end(ctx context.Context, err error) {
	if c == nil {
		return
	}

	c.entry.Status = int(status.Code(err))
	c.entry.Elapsed = time.Since(c.entry.Time)

	if c.entry.Outcome == "" {
		c.entry.Outcome = journal.OutcomeFailed
		if ctx.Err() != nil {
			c.entry.Outcome = journal.OutcomeAbandoned
		}

		if err != nil {
			c.entry.Message = err.Error()
		}
	}

	// The journal logs what it fails to keep.
	c.journal.Append(&c.entry)
}

// describeEval names the part of a call e checks, such as "header x-team".
func describeEval[T any](e *vlatte.Eval[T]) string {
	switch scope(e.Target) {
	case _scopeMethod:
		return "method"
	case _scopeHeader:
		return "header " + e.Key
	case _scopeField:
		return "field " + e.Key
	}

	return e.Key
}
