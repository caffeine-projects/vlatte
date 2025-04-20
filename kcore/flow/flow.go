// Package flow provides a lightweight middleware-style pipeline for processing byte streams in mock servers.
// It is protocol-agnostic and can be used for I/O-based request/response flows.
package flow

import (
	"io"
)

var _ io.Writer = (*Sink)(nil)
var _ Step = (*Sink)(nil)
var _ Step = (Func(nil))

// Next represents the continuation of a pipeline from the current [Step].
// It is used to advance to the next processing [Step] in the flow.
type Next interface {
	Next([]byte) (n int, err error)
}

// Step defines a single processing unit in the pipeline.
// It receives a byte buffer and a reference to the next [Step] in the flow.
// It can inspect, modify, or halt processing based on the content.
type Step interface {
	Handle(b []byte, next Next) (n int, err error)
}

// Func is an adapter to allow the use of ordinary functions as pipeline Steps.
// It enables lightweight, inline Step definitions without defining new types.
//
// Example:
//
//	flow.Func(func(b []byte, next flow.Next) (int, error) {
//	    log.Printf("Intercepted: %s", b)
//	    return next.Next(b)
//	})
type Func func(b []byte, next Next) (n int, err error)

// Handle calls the underlying function, allowing [Func] to satisfy the [Step] interface.
func (f Func) Handle(b []byte, next Next) (n int, err error) {
	return f(b, next)
}

// pipeline represents an ordered sequence of [Step]s.
// Each [Step] in the [pipeline] is executed in order, unless one returns an error
// or short-circuits the flow.
type pipeline struct {
	steps []Step
	index int
}

// New creates a new pipeline from a slice of [Step]s.
// The resulting Pipeline implements the [Next] interface and can be passed
// into Step.Handle to drive processing forward.
func New(sink Step, steps []Step) Next {
	return &pipeline{steps: append(steps, sink)}
}

func (p *pipeline) Next(b []byte) (n int, err error) {
	return p.steps[p.index].Handle(b, &pipeline{p.steps, p.index + 1})
}

// Sink is the final [Step] that writes the final output to the underlying [io.Writer].
// Typically used at the end of a pipeline to emit the transformed or
// inspected data to a mock response writer, buffer, or log.
type Sink struct{ W io.Writer }

// Handle writes the provided byte slice to the underlying [io.Writer].
// Since it's the last [Step], it will ignore the [Next] parameter.
func (s *Sink) Handle(b []byte, _ Next) (int, error) {
	return s.W.Write(b)
}

// Write makes [Sink] compatible with the [io.Writer] interface.
// It simply delegates to [Sink.Handle] with a nil [Next], making it useful
// outside of a pipeline context.
func (s *Sink) Write(p []byte) (n int, err error) {
	return s.Handle(p, nil)
}
