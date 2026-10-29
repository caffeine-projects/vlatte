package fault

import (
	"context"
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step            = timeoutStep{}
	_ vlatte.SelfDescriptive = timeoutStep{}
	_ vlatte.Stage           = (*timeoutStage)(nil)
)

// Timeout returns a step that sends nothing, then ends the answer after timeout, to test a client's read timeout
// against a server that hangs: it drops every write and flush, and aborts the flow timeout after it opened, as the
// answer began, so the connection closes. The writer doesn't wait for it, but the end of the answer does. A timeout
// of 0 never ends it: the answer hangs until the flow's context ends, as when the client leaves, with Step, or the
// server closes.
//
// With [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], the client gets no byte, not even a status
// line. With [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], the server sends the head as it cuts the
// answer: the client waits, then reads a head and the end of the connection where the body should be. It panics when
// timeout is negative.
func Timeout(timeout time.Duration) vlatte.Step {
	if timeout < 0 {
		panic(fmt.Sprintf("fault: timeout can't last %s", timeout))
	}

	return timeoutStep{timeout: timeout}
}

type timeoutStep struct{ timeout time.Duration }

// Describe implements [vlatte.SelfDescriptive], without a timeout of 0, which never ends the answer.
func (t timeoutStep) Describe() vlatte.Descriptor {
	if t.timeout == 0 {
		return vlatte.DescribeKeyValuePairs("timeout", "type", "timeout")
	}

	return vlatte.DescribeKeyValuePairs("timeout", "type", "timeout", "after", t.timeout.String())
}

func (t timeoutStep) Open(flow *vlatte.Flow, _ vlatte.Stage) vlatte.Stage {
	if t.timeout > 0 {
		abortAfter(flow, t.timeout, nil)
	}

	return &timeoutStage{ctx: flow.Context()}
}

// timeoutStage drops what it gets, and ends with its flow.
type timeoutStage struct{ ctx context.Context }

func (s *timeoutStage) Write(b []byte) (int, error) { return len(b), nil }

func (s *timeoutStage) Flush() error { return nil }

func (s *timeoutStage) Close() error {
	<-s.ctx.Done()
	return context.Cause(s.ctx)
}

// abortAfter aborts flow with err after d, unless the flow ended before: the timer ends with the flow. A d of 0
// aborts it at once.
func abortAfter(flow *vlatte.Flow, d time.Duration, err error) {
	if d == 0 {
		flow.Abort(err)
		return
	}

	t := time.AfterFunc(d, func() { flow.Abort(err) })
	context.AfterFunc(flow.Context(), func() { t.Stop() })
}
