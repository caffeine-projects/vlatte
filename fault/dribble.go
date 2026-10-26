package fault

import (
	"context"
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Step                = chunkedDribble{}
	_ descriptor.SelfDescriptive = chunkedDribble{}
	_ vlatte.Stage               = (*dribbleStage)(nil)
)

// ChunkedDribbleDelay returns a body step that sends the body in parts over total, chunks of them, to test a client
// against a slow network, deterministically: it holds the whole body until the reply wrote it, then sends part k,
// flushed, at k·total/chunks, and ends the body at total. Parts differ in size by a byte at most, and a body
// shorter than chunks has empty parts, which send nothing. An empty body ends at total.
//
// It holds the body whole, so a stream that doesn't end, such as server-sent events, never goes out. It panics when
// chunks is less than 1, or total is negative.
func ChunkedDribbleDelay(chunks int, total time.Duration) vlatte.Step {
	if chunks < 1 {
		panic(fmt.Sprintf("fault: chunked dribble delay needs a chunk at least, not %d", chunks))
	}

	if total < 0 {
		panic(fmt.Sprintf("fault: chunked dribble delay can't last %s", total))
	}

	return chunkedDribble{chunks: chunks, total: total}
}

type chunkedDribble struct {
	chunks int
	total  time.Duration
}

// Describe implements [descriptor.SelfDescriptive].
func (d chunkedDribble) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("chunked_dribble_delay", d.chunks, d.total.String())
}

func (d chunkedDribble) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &dribbleStage{ctx: flow.Context(), chunks: d.chunks, total: d.total, next: next}
}

// dribbleStage holds the body, and sends it in chunks once it ends.
type dribbleStage struct {
	ctx    context.Context
	chunks int
	total  time.Duration
	held   []byte
	next   vlatte.Stage
}

func (s *dribbleStage) Write(b []byte) (int, error) {
	s.held = append(s.held, b...)
	return len(b), nil
}

// Flush flushes what went before the body: the body goes out at the end.
func (s *dribbleStage) Flush() error { return s.next.Flush() }

func (s *dribbleStage) Close() error {
	start := time.Now()

	for k := range s.chunks {
		if err := s.wait(start, k); err != nil {
			return err
		}

		part := s.held[k*len(s.held)/s.chunks : (k+1)*len(s.held)/s.chunks]
		if len(part) == 0 {
			continue
		}

		if _, err := s.next.Write(part); err != nil {
			return err
		}

		if err := s.next.Flush(); err != nil {
			return err
		}
	}

	if err := s.wait(start, s.chunks); err != nil {
		return err
	}

	return s.next.Close()
}

// wait waits until k·total/chunks after start, or the flow's end.
func (s *dribbleStage) wait(start time.Time, k int) error {
	return waitUntil(s.ctx, start.Add(s.total*time.Duration(k)/time.Duration(s.chunks)))
}

// waitUntil waits until t, or the end of ctx, whose cause it returns.
func waitUntil(ctx context.Context, t time.Time) error {
	timer := time.NewTimer(time.Until(t))
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
