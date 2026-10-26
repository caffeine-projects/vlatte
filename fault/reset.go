package fault

import (
	"context"
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Step                = resetByPeer{}
	_ descriptor.SelfDescriptive = resetByPeer{}
	_ vlatte.Stage               = (*resetStage)(nil)
	_ vlatte.Step                = resetAfter{}
	_ descriptor.SelfDescriptive = resetAfter{}
	_ vlatte.Stage               = (*resetAfterStage)(nil)
)

// ConnectionResetByPeer returns a wire step that resets the connection: at the first byte of the answer, or at its
// end, it aborts the flow with [vlatte.ErrReset], so the connection closes with a TCP reset before a status line. The
// client reads a reset, such as syscall.ECONNRESET, not an end.
func ConnectionResetByPeer() vlatte.Step {
	return resetByPeer{}
}

type resetByPeer struct{}

// Describe implements [descriptor.SelfDescriptive].
func (resetByPeer) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly(_fConnectionResetByPeer)
}

func (resetByPeer) Open(flow *vlatte.Flow, _ vlatte.Stage) vlatte.Stage {
	return &resetStage{flow: flow}
}

// resetStage aborts its flow with a reset at its first call.
type resetStage struct{ flow *vlatte.Flow }

func (s *resetStage) Write([]byte) (int, error) { return 0, s.reset() }

func (s *resetStage) Flush() error { return s.reset() }

func (s *resetStage) Close() error { return s.reset() }

func (s *resetStage) reset() error {
	s.flow.Abort(vlatte.ErrReset)
	return context.Cause(s.flow.Context())
}

// ConnectionResetByPeerAfter returns a step that passes the answer on as it is, then resets the connection after d: it
// aborts the flow with [vlatte.ErrReset] d after it opened, as the answer began, to test a client that a reset meets
// while it reads. An answer that ends before d is flushed whole, then waits for the reset, so a client that reads
// until the end reads a reset. A d of 0 resets at once, as [ConnectionResetByPeer] does.
//
// It goes with [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], or with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step] over HTTP/1, where a reset resets the connection too.
// It panics when d is negative.
func ConnectionResetByPeerAfter(d time.Duration) vlatte.Step {
	if d < 0 {
		panic(fmt.Sprintf("fault: connection reset by peer can't come %s after", d))
	}

	return resetAfter{d: d}
}

type resetAfter struct{ d time.Duration }

// Describe implements [descriptor.SelfDescriptive].
func (r resetAfter) Describe() descriptor.Descriptor {
	return descriptor.SingleArg(_fConnectionResetByPeerAfter, r.d.String())
}

func (r resetAfter) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	abortAfter(flow, r.d, vlatte.ErrReset)
	return &resetAfterStage{ctx: flow.Context(), next: next}
}

// resetAfterStage passes what it gets on, and holds the end until its flow ends.
type resetAfterStage struct {
	ctx  context.Context
	next vlatte.Stage
}

func (s *resetAfterStage) Write(b []byte) (int, error) { return s.next.Write(b) }

func (s *resetAfterStage) Flush() error { return s.next.Flush() }

func (s *resetAfterStage) Close() error {
	if err := s.next.Flush(); err != nil {
		return err
	}

	<-s.ctx.Done()

	return context.Cause(s.ctx)
}
