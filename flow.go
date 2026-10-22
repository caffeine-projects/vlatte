package vlatte

import (
	"context"
	"io"
	"slices"
)

var (
	_ io.Writer = (*Sink)(nil)
	_ Step      = (*Sink)(nil)
	_ Step      = StepFunc(nil)
	_ Next      = (*link)(nil)
	_ Next      = end{}
)

// Next is the rest of a flow after a [Step]: the steps after it, then the sink.
// Next passes b to the next step and returns what that step returns. Through steps that pass their buffer on whole,
// that is what the sink wrote, which is not len(b) once a step changed the bytes.
// Next can be called any number of times, as by a step that passes its buffer on in parts: each call passes its
// buffer through all of the rest of the flow.
type Next interface {
	Next(b []byte) (n int, err error)
}

// Step is a stage of a flow. Handle gets each buffer the stage before it passes on, and decides what goes on: it can
// pass b to next as it is, pass it on changed or in parts, delay it, drop it by not calling next, or fail the write
// by returning an error, which next returns to the step before it.
// Handle calls next one call at a time, and never once it returned: the sink, such as a response writer, can't be
// written to concurrently or later.
// b is the caller's, as for [io.Writer]: Handle must not change its bytes, nor keep it once it returned. To change
// the bytes, it passes on a new slice, such as append returns: the flow clips b, so append always copies it.
// Steps can be shared, such as a mock's steps across its requests: Handle must be safe for concurrent use.
type Step interface {
	Handle(ctx context.Context, b []byte, next Next) (n int, err error)
}

// StepFunc is an adapter to allow the use of ordinary functions as pipeline Steps.
//
// Example:
//
//	StepFunc(func(ctx context.Context, b []byte, next Next) (int, error) {
//	    select {
//	    case <-ctx.Done():
//	        return 0, ctx.Err()
//	    default:
//	        return next.Next(b)
//	    }
//	})
type StepFunc func(ctx context.Context, b []byte, next Next) (n int, err error)

// Handle calls the underlying function, allowing [StepFunc] to satisfy the [Step] interface.
func (f StepFunc) Handle(ctx context.Context, b []byte, next Next) (n int, err error) {
	return f(ctx, b, next)
}

// Flow returns the start of a flow that passes a buffer through steps, in order, then to sink. Every step gets ctx,
// typically the request's context, so it can stop when the client leaves or a deadline passes.
// The sink ends the flow: the [Next] it gets returns [io.EOF].
// Flow neither keeps nor changes steps, so they can be shared, such as a mock's steps across concurrent requests.
func Flow(ctx context.Context, sink Step, steps []Step) Next {
	links := make([]link, len(steps)+1)
	links[len(steps)] = link{ctx: ctx, step: sink, next: end{}}

	for i, step := range slices.Backward(steps) {
		links[i] = link{ctx: ctx, step: step, next: &links[i+1]}
	}

	return &links[0]
}

// link is a step of a flow, with next as the rest of the flow.
type link struct {
	ctx  context.Context
	step Step
	next Next
}

// Next clips b, so a step that appends to it can't write over what follows b in its array, such as the next part of
// a step that passes its buffer on in parts.
func (l *link) Next(b []byte) (int, error) {
	return l.step.Handle(l.ctx, slices.Clip(b), l.next)
}

// end is what follows the sink: nothing.
type end struct{}

func (end) Next([]byte) (int, error) { return 0, io.EOF }

// Sink is the final [Step] that writes the final output to the underlying [io.Writer].
type Sink struct{ W io.Writer }

// Handle writes the provided byte slice to the underlying [io.Writer].
func (s *Sink) Handle(_ context.Context, b []byte, _ Next) (int, error) {
	return s.W.Write(b)
}

// Write writes p to W and returns what W returns, as Handle does, so a Sink is also an [io.Writer].
func (s *Sink) Write(p []byte) (n int, err error) {
	return s.Handle(context.Background(), p, nil)
}
