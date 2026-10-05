package vlatte

import (
	"context"
	"io"
)

var (
	_ io.Writer = (*Sink)(nil)
	_ Step      = (*Sink)(nil)
	_ Step      = StepFunc(nil)
)

// Next represents the continuation of a pipeline from the current [Step].
// It is used to advance to the next processing [Step] in the flow.
type Next interface {
	Next([]byte) (n int, err error)
}

// Step defines a single processing unit in the pipeline.
// It receives a context (for cancellation/timeouts), a byte buffer, and the next [Step].
// It can inspect, modify, or halt processing based on the content or context state.
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

// pipeline represents an ordered sequence of [Step]s and carries the [context.Context]
// so each step can react to cancellation or deadlines (e.g. timeout simulation).
type pipeline struct {
	ctx   context.Context
	steps []Step
	index int
}

// Flow creates a new pipeline from a [context.Context] and a slice of [Step]s.
// The context is passed to every step's Handle and is typically the request context,
// so steps can respect client disconnect or timeouts.
func Flow(ctx context.Context, sink Step, steps []Step) Next {
	return &pipeline{ctx: ctx, steps: append(steps, sink)}
}

func (p *pipeline) Next(b []byte) (n int, err error) {
	if p.index >= len(p.steps) {
		return 0, io.EOF
	}
	i := p.index
	p.index++
	return p.steps[i].Handle(p.ctx, b, p)
}

// Sink is the final [Step] that writes the final output to the underlying [io.Writer].
type Sink struct{ W io.Writer }

// Handle writes the provided byte slice to the underlying [io.Writer].
func (s *Sink) Handle(_ context.Context, b []byte, _ Next) (int, error) {
	return s.W.Write(b)
}

// Write makes [Sink] compatible with the [io.Writer] interface.
// When called directly (outside a pipeline), context.Background() is used —
// cancellation and deadlines are not propagated. Drive the pipeline through [Next] to preserve context.
func (s *Sink) Write(p []byte) (n int, err error) {
	return s.Handle(context.Background(), p, nil)
}
