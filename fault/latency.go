package fault

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step  = latency{}
	_ vlatte.Stage = (*latencyStage)(nil)
)

// Latency returns a step that delays every write and flush by what delay picks for it, such as [vlatte.Latency] or
// [vlatte.Jitter], to test a client against a distant server: each reaches the client that long after the writer
// sent it, in order, as over a link with that latency. The writer doesn't wait: the answer keeps its pace, only
// later, and a stream whose writes come a second apart still gets them a second apart. With a delay that varies, a
// write waits for those before it, so nothing comes out of order. The end of the answer comes once what was sent
// before it did.
//
// It goes with [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], to delay the body, or with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], to delay the whole answer, its head included.
// delay is called for each write and flush of every answer of the mock at once: it must be safe for concurrent use,
// as [vlatte.Latency] and [vlatte.Jitter] are. It panics when delay is nil.
func Latency(delay vlatte.Delay) vlatte.Step {
	if delay == nil {
		panic("fault: latency needs a delay")
	}

	return latency{delay: delay}
}

type latency struct{ delay vlatte.Delay }

func (l latency) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	s := &latencyStage{
		ctx:   flow.Context(),
		delay: l.delay,
		next:  next,
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
	}

	go s.run()

	return s
}

// latencyOp is what a latencyStage passes on at: a write of b, a flush, or the end.
type latencyOp struct {
	at    time.Time
	b     []byte
	flush bool
	close bool
}

// latencyStage queues what it gets, each with the time it is due, and passes it on, in order, from a goroutine of its
// own, which ends with the flow, the end of the answer, or the first error of next.
type latencyStage struct {
	ctx   context.Context
	delay vlatte.Delay
	next  vlatte.Stage
	wake  chan struct{} // a new op is queued
	done  chan struct{} // run ended

	mu    sync.Mutex
	queue []latencyOp
	err   error // what ended run
}

func (s *latencyStage) Write(b []byte) (int, error) {
	if err := s.push(latencyOp{at: time.Now().Add(s.delay()), b: bytes.Clone(b)}); err != nil {
		return 0, err
	}

	return len(b), nil
}

func (s *latencyStage) Flush() error {
	return s.push(latencyOp{at: time.Now().Add(s.delay()), flush: true})
}

func (s *latencyStage) Close() error {
	if err := s.push(latencyOp{at: time.Now(), close: true}); err != nil {
		return err
	}

	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.err
}

// push queues op, unless run ended, and returns why it did.
func (s *latencyStage) push(op latencyOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}

	s.queue = append(s.queue, op)

	select {
	case s.wake <- struct{}{}:
	default:
	}

	return nil
}

func (s *latencyStage) run() {
	defer close(s.done)

	err := s.passOn()

	s.mu.Lock()
	s.err = err
	s.queue = nil
	s.mu.Unlock()
}

// passOn passes each op on once it is due, until the end, which it returns the error of.
func (s *latencyStage) passOn() error {
	for {
		op, err := s.pop()
		if err != nil {
			return err
		}

		if err := waitUntil(s.ctx, op.at); err != nil {
			return err
		}

		switch {
		case op.close:
			return s.next.Close()
		case op.flush:
			err = s.next.Flush()
		default:
			_, err = s.next.Write(op.b)
		}

		if err != nil {
			return err
		}
	}
}

// pop takes the first op of the queue, once there is one, or returns the cause of the end of the flow.
func (s *latencyStage) pop() (latencyOp, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			op := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()

			return op, nil
		}
		s.mu.Unlock()

		select {
		case <-s.wake:
		case <-s.ctx.Done():
			return latencyOp{}, context.Cause(s.ctx)
		}
	}
}
