package fault

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/delay"
)

var (
	_ vlatte.Pipe            = latencyPipe{}
	_ vlatte.SelfDescriptive = latencyPipe{}
	_ vlatte.Stage           = (*latencyStage)(nil)
)

// Latency delays each write and flush by a duration chosen by d, preserving their order. The writer does not wait:
// the answer keeps its pace, only later. The end waits for the queued writes. Nil adds no delay. Evaluation errors
// are returned by Write or Flush. It goes with a mock's body Pipe or WirePipe to include the response headers.
func Latency(d vlatte.Delay) vlatte.Pipe {
	if d == nil {
		d = vlatte.Latency(0)
	}
	return latencyPipe{delay: d}
}

type latencyPipe struct {
	delay vlatte.Delay
}

// Describe implements [vlatte.SelfDescriptive], preserving the delay configuration and description errors.
func (l latencyPipe) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyValuePairs("latency", "type", "latency", "delay", delay.Doc(l.delay))
}

func (l latencyPipe) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	s := &latencyStage{
		flow:  flow,
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
// own, which ends with the flow, the end of the answer, or the first error of next, which aborts the flow when it asks
// to.
type latencyStage struct {
	flow  *vlatte.Flow
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
	d, err := delay.Next(s.ctx, s.delay)
	if err != nil {
		return 0, err
	}
	if err := s.push(latencyOp{at: time.Now().Add(d), b: bytes.Clone(b)}); err != nil {
		return 0, err
	}

	return len(b), nil
}

func (s *latencyStage) Flush() error {
	d, err := delay.Next(s.ctx, s.delay)
	if err != nil {
		return err
	}
	return s.push(latencyOp{at: time.Now().Add(d), flush: true})
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

	// The flow's caller doesn't see what next returns from here: an abort must end the flow itself.
	if errors.Is(err, vlatte.ErrAbort) || errors.Is(err, vlatte.ErrReset) {
		s.flow.Abort(err)
	}

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
