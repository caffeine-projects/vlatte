package vlatte

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
)

var (
	// ErrAbort is the cause of a flow a stage aborted, with [Flow.Abort] or by returning an error that matches it.
	// The server cuts the stream: an HTTP/1 connection is closed, and an HTTP/2 stream is reset.
	ErrAbort = errors.New("flow: aborted")

	// ErrReset aborts a flow, as [ErrAbort] does, with a TCP reset where the connection allows one: its unsent data
	// is dropped and the peer reads a reset, not an end. Elsewhere, such as over HTTP/2, it cuts the stream.
	ErrReset = errors.New("flow: reset")
)

// errClosed is the cause of a flow once it is closed.
var errClosed = errors.New("flow: closed")

var (
	_ Stage = (*Flow)(nil)
	_ Stage = (*Sink)(nil)
	_ Stage = (*funcStage)(nil)
	_ Stage = clipped{}
	_ Stage = sinkStage{}
	_ Pipe  = PipeFunc(nil)
)

// Stage is a pipe's part of one [Flow]. Write follows [io.Writer]: n is how much of b the stage took, which is len(b)
// when it passed b on changed, in parts, later, or not at all.
//
// A stage decides what goes on to next: b as it is, changed or in parts, later, or nothing. It fails the flow by
// returning an error. One that matches [ErrAbort] or [ErrReset] aborts it. Flush passes on what the stage holds,
// then flushes next. Close passes on what it holds, then closes next.
//
// The flow's caller calls a stage one call at a time. A stage may call next from a goroutine of its own, as one that
// delays its writes does: its calls never overlap, and all of them end before its Close returns.
// b is the caller's, as for [io.Writer]: a stage must not change its bytes, and copies it to keep it once Write
// returned. The flow clips every b, so append always copies it.
// Once the flow's [Flow.Context] ends, a stage drops what it holds, returns from Close at once, and its goroutines
// and timers end: what it passes on from then on doesn't reach the sink.
type Stage interface {
	Write(b []byte) (n int, err error)
	Flush() error
	Close() error
}

// Pipe makes its [Stage] of each [Flow] it is part of, in front of next: the rest of the flow. A pipe keeps what
// lasts the whole flow in its stage, such as a count of the bytes it passed. A mock's pipes make the stages of all
// its responses: Open must be safe for concurrent use.
//
// flow gives the stage the flow's context, and a way to abort it from a timer: see [Flow.Abort].
type Pipe interface {
	Open(flow *Flow, next Stage) Stage
}

// PipeFunc is a [Pipe] with no state, an adapter for ordinary functions: its stage calls it for each write, with the
// flow's context, and passes Flush and Close on.
//
// Example:
//
//	PipeFunc(func(ctx context.Context, b []byte, next Stage) (int, error) {
//	    if _, err := next.Write(bytes.ToUpper(b)); err != nil {
//	        return 0, err
//	    }
//
//	    return len(b), nil
//	})
type PipeFunc func(ctx context.Context, b []byte, next Stage) (n int, err error)

// Open makes the stage of f in flow, in front of next.
func (f PipeFunc) Open(flow *Flow, next Stage) Stage {
	return &funcStage{fn: f, ctx: flow.Context(), next: next}
}

// Flow is one stream through pipes: a response body, the bytes of a response on the wire, a request body, one
// direction of a connection. Each pipe has its [Stage] in it, and the sink comes last.
//
// A flow is a [Stage] itself: its caller calls Write, Flush and Close, one call at a time, and Close once the stream
// ends. Once its [Flow.Context] ends, Write and Flush return the cause.
type Flow struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	head   Stage
	closed bool
}

// NewFlow opens the stages of pipes in a new flow, in front of sink, from the last pipe to the first. The flow's
// context derives from ctx, typically the request's, so its stages stop when the client leaves. Pipes can be shared,
// such as a mock's pipes across its concurrent requests: NewFlow neither keeps nor changes pipes.
func NewFlow(ctx context.Context, sink Stage, pipes []Pipe) *Flow {
	f := &Flow{}
	f.ctx, f.cancel = context.WithCancelCause(ctx)

	next := Stage(sinkStage{flow: f, sink: sink})
	for _, pipe := range slices.Backward(pipes) {
		next = clipped{pipe.Open(f, next)}
	}

	f.head = next

	return f
}

// Context returns the flow's context. It ends when the context the flow was made with does, when a stage aborts the
// flow, or once the flow is closed. context.Cause tells which: a cause that matches [ErrAbort] is an abort.
func (f *Flow) Context() context.Context {
	return f.ctx
}

// Abort aborts the flow, from any goroutine, such as a stage's timer: it ends the flow's context with a cause that
// matches [ErrAbort] and err, which may be nil. Pass [ErrReset] for a reset. The first end wins: once the flow ended,
// Abort does nothing.
func (f *Flow) Abort(err error) {
	switch {
	case err == nil:
		err = ErrAbort
	case !errors.Is(err, ErrAbort):
		err = fmt.Errorf("%w: %w", ErrAbort, err)
	}

	f.cancel(err)
}

// Write passes b to the first stage. An error that matches [ErrAbort] or [ErrReset] aborts the flow.
func (f *Flow) Write(b []byte) (int, error) {
	if err := context.Cause(f.ctx); err != nil {
		return 0, err
	}

	n, err := f.head.Write(b)

	return n, f.check(err)
}

// Flush has the stages pass on what they hold, then flushes the sink.
func (f *Flow) Flush() error {
	if err := context.Cause(f.ctx); err != nil {
		return err
	}

	return f.check(f.head.Flush())
}

// Close ends the stream: the stages pass on what they hold and end, in order, then the sink. Then the flow's context
// ends. Close returns once the last stage did, so nothing reaches the sink after it. Closing again does nothing.
func (f *Flow) Close() error {
	if f.closed {
		return nil
	}

	f.closed = true
	err := f.check(f.head.Close())
	f.cancel(errClosed)

	return err
}

// check aborts the flow when err asks to.
func (f *Flow) check(err error) error {
	if errors.Is(err, ErrAbort) || errors.Is(err, ErrReset) {
		f.Abort(err)
	}

	return err
}

// funcStage is the stage of a PipeFunc in a flow.
type funcStage struct {
	fn   PipeFunc
	ctx  context.Context
	next Stage
}

func (s *funcStage) Write(b []byte) (int, error) { return s.fn(s.ctx, b, s.next) }

func (s *funcStage) Flush() error { return s.next.Flush() }

func (s *funcStage) Close() error { return s.next.Close() }

// clipped passes every buffer to s clipped, so a stage that appends to one can't write over what follows it in its
// array, such as the next part of a stage that passes its buffer on in parts.
type clipped struct{ s Stage }

func (c clipped) Write(b []byte) (int, error) { return c.s.Write(slices.Clip(b)) }

func (c clipped) Flush() error { return c.s.Flush() }

func (c clipped) Close() error { return c.s.Close() }

// sinkStage passes to the sink of flow, clipped, while the flow is on: once it ended, such as by an abort, what a stage
// still passes on reaches nothing.
type sinkStage struct {
	flow *Flow
	sink Stage
}

func (s sinkStage) Write(b []byte) (int, error) {
	if err := context.Cause(s.flow.ctx); err != nil {
		return 0, err
	}

	return s.sink.Write(slices.Clip(b))
}

func (s sinkStage) Flush() error {
	if err := context.Cause(s.flow.ctx); err != nil {
		return err
	}

	return s.sink.Flush()
}

func (s sinkStage) Close() error { return s.sink.Close() }

// Sink is a [Stage] that ends a flow: it writes what it gets to W.
type Sink struct{ W io.Writer }

// Write writes p to W and returns what W returns, so a Sink is also an [io.Writer].
func (s *Sink) Write(p []byte) (n int, err error) {
	return s.W.Write(p)
}

// Flush flushes W when it can, with a Flush() error method, such as a [bufio.Writer] has.
func (s *Sink) Flush() error {
	if f, ok := s.W.(interface{ Flush() error }); ok {
		return f.Flush()
	}

	return nil
}

// Close does nothing: W is its owner's to close.
func (s *Sink) Close() error {
	return nil
}
