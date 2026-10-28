package mockgrpc

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

// serve matches call with the server's mocks, and answers it with the first that matches: the path of every call,
// whatever its type. It returns what the call ends with: nil, the status of the mock's answer, or a failure.
//
// The scenario of the mock moves when the mock is found, before its delay. Then, in order: the delay, the reply, then,
// once it answered, with messages, a status or a cut, the state of its stateful matchers, such as Times, and its
// events. The mock counts the call as its answer starts going out, so the debug header carries the count: a reply that
// fails, or a client that leaves, takes it back, and counts nothing. A call whose context ends before a mock is found
// ends with the code of its context.
func (in *interceptors) serve(ctx context.Context, call *Call) error {
	app := in.app
	kind := typeOf(call.desc)

	journaled := callOf(ctx)
	journaled.serve(ctx)

	body, err := call.encode()
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: %v", kind, err)
	}

	call.json = body

	// A bidi call's messages come as its reply reads them: its entry gets them once the reply returns.
	if kind != _bidiStream {
		journaled.request(body)
	}

	// A recording captures the answer as the client gets it. A recording with a target sends it what it takes, before
	// any mock.
	rec := app.recordingOf(call)

	var cs *captureStream
	if rec != nil {
		ctx, cs = capture(ctx)

		if rec.target != nil {
			return in.serveTarget(ctx, call, rec, cs)
		}
	}

	stored, err := app.storage.List(ctx)
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: listing mocks: %v", kind, err)
	}

	// Only the journal reports near misses: a call's status carries no more than its message. They go on from where
	// matching stopped, so no matcher runs twice for the call.
	var misses *vlatte.Misses[*Mock, *Call]
	if journaled != nil && !app.config.NoNearMisses {
		misses = new(vlatte.Misses[*Mock, *Call])
	}

	mocks := vlatte.Mocks[*Mock, *Call](stored)
	evals := func(m *Mock) vlatte.Evaluators[*Mock, *Call] { return m.expectations }

	mock, pass, err := mocks.Find(ctx, vlatte.NewExplanation(), call, evals, misses)
	if err != nil && ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}

	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: finding a mock: %v", kind, err)
	}

	if !pass {
		var near []vlatte.NearMiss
		if misses != nil {
			near = misses.NearMisses(ctx, describeEval)
		}

		journaled.unmatched(near)
		app.publish(ctx, adminapi.EventUnmatched, call.method, nil, app.config.NoMatchCode, nil)
		app.logger.Warn().Str("method", call.method).Msg("grpc: no mock matched the call")

		return fail(ctx, vlatte.FailureNoMatch, app.config.NoMatchCode, "%s: no mock matched the call of %s", kind,
			call.method)
	}

	journaled.matched(mock, app.movedScenarios(ctx, mock))

	// The template data providers see the mock that matched. One that fails fails the call: its mock doesn't answer.
	if err := app.provide(call, mock); err != nil {
		journaled.replied(ctx, err)
		app.publish(ctx, adminapi.EventError, call.method, mock, codes.Internal, err)

		return fail(ctx, vlatte.FailureReply, codes.Internal, "%s: %s: %v", kind, mock, err)
	}

	// A client that leaves during the delay gets nothing: the mock doesn't reply, nor count the call. Its scenario moved
	// already, when Find picked it.
	if d := mock.wait(); d > 0 {
		if err := httpx.Wait(ctx, d); err != nil {
			return status.FromContextError(err).Err()
		}
	}

	h := &hit{ctx: ctx, mock: mock, debug: !app.config.NoDebugHeaders}
	call.hit = h

	err = mock.replier.Reply(grpc.NewContextWithServerTransportStream(ctx, hitStream{grpc.ServerTransportStreamFromContext(ctx), h}), call)

	// A bidi call whose messages can't be written as JSON is journaled without them: its entry says it failed.
	var encErr error
	if kind == _bidiStream {
		var b []byte
		if b, encErr = call.encode(); encErr == nil {
			journaled.request(b)
		}
	}

	journaled.replied(ctx, err)

	if encErr != nil {
		journaled.failed(fmt.Errorf("%s: %w", kind, encErr))
	}

	// A reply that cut the call answered it, as long as its client is there.
	if cuts(err) && ctx.Err() == nil {
		return in.cut(ctx, call, mock, h, err)
	}

	if err != nil && !hasStatus(err) {
		h.takeBack()
		app.publish(ctx, adminapi.EventError, call.method, mock, codes.Internal, err)

		return replyError(ctx, kind.String(), mock, err)
	}

	// A client that left during the reply, such as between the messages of a stream, got no whole answer: the mock
	// doesn't count the call.
	if err != nil && ctx.Err() != nil {
		h.takeBack()
		return err
	}

	if rec != nil {
		rec.record(call, cs, err)
	}

	// A reply that sent nothing yet answers with its status, or a unary call's message, which go out now.
	h.count()
	app.afterServed(ctx, mock, call, err)
	app.publish(ctx, adminapi.EventMatched, call.method, mock, status.Code(err), nil)

	return err
}

// serveTarget answers call with the answer of the target of rec, the recording, which records it: no mock answers,
// and none counts the call. cs keeps the header and trailer of the answer.
func (in *interceptors) serveTarget(ctx context.Context, call *Call, rec *recording, cs *captureStream) error {
	kind := typeOf(call.desc)
	journaled := callOf(ctx)

	err := rec.target.Reply(ctx, call)

	if kind == _bidiStream {
		if b, encErr := call.encode(); encErr == nil {
			journaled.request(b)
		}
	}

	journaled.proxied(ctx, err)

	if err != nil && !hasStatus(err) {
		in.app.logger.Warn().Err(err).Str("method", call.method).Msg("recorder: the target failed the call")
		return fail(ctx, vlatte.FailureReply, codes.Internal, "%s: recorder: %v", kind, err)
	}

	// A client that left got no whole answer: the recording doesn't take it.
	if ctx.Err() == nil {
		rec.record(call, cs, err)
	}

	return err
}

// afterServed runs what follows a mock's answer, which ended with err, a status or nil: its stateful matchers move
// on, then its events run, and the call ends after them, but for those set to run after the response.
func (app *Server) afterServed(ctx context.Context, mock *Mock, call *Call, err error) {
	for i, e := range mock.expectations {
		if stateful, ok := e.Matcher.(vlatte.Stateful); ok {
			if err := stateful.SetState(); err != nil {
				app.logger.Warn().Err(err).Str("mock", mock.ID()).Msgf("grpc: matcher %d: setting its state", i)
			}
		}
	}

	st := status.Convert(err)

	app.runner.OnComplete(
		ctx,
		mock,
		func(ctx context.Context, cfg vlatte.EventConfig) vlatte.OnComplete {
			// The call's context ends when the client leaves: templates read the parameters with the event's.
			data := app.templateData(ctx, call)

			return &OnComplete{
				m:      mock,
				call:   call,
				status: st,
				kit: &Kit{
					engine:    app.templateEngine(),
					data:      data,
					logger:    app.logger,
					transport: app.webhooks,
					dir:       cfg.Dir,
				},
				args:   cfg.Parameters,
				dir:    cfg.Dir,
				logger: app.logger,
			}
		},
		func(err error) {
			app.logger.Warn().Err(err).Str("mock", mock.ID()).Str("method", call.method).Msg("grpc: on_complete")
			callOf(ctx).eventFailed(err)
		},
	)
}

// unknown answers a call of method, which no descriptor of the server describes, so no mock can serve it.
func (in *interceptors) unknown(ctx context.Context, method string) error {
	journaled := callOf(ctx)
	journaled.serve(ctx)
	journaled.unmatched(nil)

	in.app.publish(ctx, adminapi.EventUnmatched, method, nil, codes.Unimplemented, nil)
	in.app.logger.Warn().Str("method", method).Msg("grpc: no descriptor describes the method of the call")

	return fail(ctx, vlatte.FailureNoMatch, codes.Unimplemented, "grpc: unknown method %s: import the generated "+
		"code of its service, or give the server its descriptors with Setup().Descriptors", method)
}

// incoming returns the metadata the client of ctx sent.
func incoming(ctx context.Context) metadata.MD {
	md, _ := metadata.FromIncomingContext(ctx)
	return md
}

// unknownService is the handler grpc gives the calls of services it has no implementation of: the stream
// interceptor serves them all, so it never runs.
func unknownService(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	return status.Error(codes.Unimplemented, fmt.Sprintf("grpc: unknown method %s", method))
}

// hit counts a call of mock once the answer starts going out: with its header, its first message of a stream, or its
// status. The debug header goes out with it, and carries the count then. A call that fails, or whose client leaves,
// after its answer started takes the hit back: the header went out, and can't.
type hit struct {
	ctx     context.Context // the call's, whose stream sets the header
	mock    *Mock
	debug   bool // the server sends debug headers
	once    sync.Once
	counted bool
}

// count counts the call, once. A call no mock answers, such as one the target of a recording answers, has no hit.
func (h *hit) count() {
	if h == nil {
		return
	}

	h.once.Do(func() {
		h.counted = true

		hits := h.mock.hits.Add(1)
		if h.debug {
			_ = grpc.SetHeader(h.ctx, debugHeader(h.mock, hits))
		}
	})
}

// takeBack uncounts the call, if it was counted, and keeps a reply that goes on from counting it.
func (h *hit) takeBack() {
	h.once.Do(func() {})

	if h.counted {
		h.mock.hits.Add(-1)
	}
}

// hitStream is the stream of a call's reply: it counts the call as its header goes out.
type hitStream struct {
	grpc.ServerTransportStream

	hit *hit
}

// SendHeader counts the call, then sends md.
func (s hitStream) SendHeader(md metadata.MD) error {
	s.hit.count()
	return s.ServerTransportStream.SendHeader(md)
}
