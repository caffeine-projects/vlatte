package fault

import (
	"context"
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step            = slowClose{}
	_ vlatte.SelfDescriptive = slowClose{}
	_ vlatte.Stage           = (*slowCloseStage)(nil)
)

// SlowClose returns a step that delays the end of the answer by delay, as a server slow to close its connection does:
// it passes the answer on as it is, flushes it at its end, then waits delay before it ends it. The client has every
// byte by then, and waits only when it reads until the end.
//
// With [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], the end of the body waits: the last chunk of a
// chunked body. With [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], the close of the connection
// waits, after the last chunk: a body that ends with the connection, as over HTTP/1.0, waits for it. It panics when
// delay is negative.
func SlowClose(delay time.Duration) vlatte.Step {
	if delay < 0 {
		panic(fmt.Sprintf("fault: slow close can't last %s", delay))
	}

	return slowClose{delay: delay}
}

type slowClose struct{ delay time.Duration }

// Describe implements [vlatte.SelfDescriptive].
func (c slowClose) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyValuePairs("slow_close", "type", "slow_close", "delay", c.delay.String())
}

func (c slowClose) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &slowCloseStage{ctx: flow.Context(), delay: c.delay, next: next}
}

// slowCloseStage passes what it gets on, and waits before the end.
type slowCloseStage struct {
	ctx   context.Context
	delay time.Duration
	next  vlatte.Stage
}

func (s *slowCloseStage) Write(b []byte) (int, error) { return s.next.Write(b) }

func (s *slowCloseStage) Flush() error { return s.next.Flush() }

func (s *slowCloseStage) Close() error {
	if err := s.next.Flush(); err != nil {
		return err
	}

	if err := waitUntil(s.ctx, time.Now().Add(s.delay)); err != nil {
		return err
	}

	return s.next.Close()
}
