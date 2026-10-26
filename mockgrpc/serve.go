package mockgrpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

// serve matches call with the server's mocks, and answers it with the first that matches: the path of every call,
// whatever its type. It returns what the call ends with: nil, the status of the mock's answer, or a failure.
//
// In order: the mock's delay, its reply, then, once it answered, with messages or a status, its hit, the state of its
// stateful matchers, such as Times and scenarios, and its events. A reply that fails counts nothing.
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
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: finding a mock: %v", kind, err)
	}

	if !pass {
		var near []vlatte.NearMiss
		if misses != nil {
			near = misses.NearMisses(ctx, describeEval)
		}

		journaled.unmatched(near)
		app.publish(ctx, handler.EventUnmatched, call.method, nil, app.config.NoMatchCode, nil)
		app.logger.Warn().Str("method", call.method).Msg("grpc: no mock matched the call")

		return fail(ctx, vlatte.FailureNoMatch, app.config.NoMatchCode, "%s: no mock matched the call of %s", kind,
			call.method)
	}

	journaled.matched(mock)

	// The header goes out with the reply's own, or with its status.
	if !app.config.NoDebugHeaders {
		_ = grpc.SetHeader(ctx, debugHeader(mock))
	}

	// A client that leaves during the delay gets nothing: the mock doesn't reply, nor count the call.
	if d := mock.wait(); d > 0 {
		if err := httpx.Wait(ctx, d); err != nil {
			return status.FromContextError(err).Err()
		}
	}

	err = mock.replier.Reply(ctx, call)

	if kind == _bidiStream {
		if b, encErr := call.encode(); encErr == nil {
			journaled.request(b)
		}
	}

	journaled.replied(ctx, err)

	if err != nil && !hasStatus(err) {
		app.publish(ctx, handler.EventError, call.method, mock, codes.Internal, err)
		return replyError(ctx, kind.String(), mock, err)
	}

	// A client that left during the reply, such as between the messages of a stream, got no whole answer: the mock
	// doesn't count the call.
	if err != nil && ctx.Err() != nil {
		return err
	}

	mock.Inc()
	app.afterServed(ctx, mock, call, err)
	app.publish(ctx, handler.EventMatched, call.method, mock, status.Code(err), nil)

	return err
}

// afterServed runs what follows a mock's answer, which ended with err, a status or nil: its stateful matchers move
// on, then its events run, and the call ends after them.
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
				kit:    &Kit{engine: app.templateEngine(), data: data},
				args:   cfg.Parameters,
				dir:    cfg.Dir,
				logger: app.logger,
			}
		},
		func(err error) {
			app.logger.Warn().Err(err).Str("mock", mock.ID()).Str("method", call.method).Msg("grpc: on_complete")
		},
	)
}

// unknown answers a call of method, which no descriptor of the server describes, so no mock can serve it.
func (in *interceptors) unknown(ctx context.Context, method string) error {
	journaled := callOf(ctx)
	journaled.serve(ctx)
	journaled.unmatched(nil)

	in.app.publish(ctx, handler.EventUnmatched, method, nil, codes.Unimplemented, nil)
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
