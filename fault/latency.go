package fault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Pipe            = latencyPipe{}
	_ vlatte.SelfDescriptive = latencyPipe{}
	_ vlatte.Stage           = (*latencyStage)(nil)
)

// Latency returns a pipe that delays every write and flush by latency, give or take jitter at random, to test a client
// against a distant server: each reaches the client from latency-jitter to latency+jitter after the writer sent it,
// in order, as over a link with that latency. The writer doesn't wait: the answer keeps its pace, only later, and a
// stream whose writes come a second apart still gets them a second apart. With jitter, a write waits for those
// before it, so nothing comes out of order. The end of the answer comes once what was sent before it did.
//
// It goes with [github.com/caffeine-projects/vlatte/mockhttp.Builder.Pipe], to delay the body, or with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WirePipe], to delay the whole answer, its head included. It
// panics when latency is negative, or jitter is negative or more than latency.
func Latency(latency, jitter time.Duration) vlatte.Pipe {
	if latency < 0 {
		panic(fmt.Sprintf("fault: latency can't be %s", latency))
	}

	if jitter < 0 || jitter > latency {
		panic(fmt.Sprintf("fault: latency jitter must be from 0 to %s, not %s", latency, jitter))
	}

	return latencyPipe{latency: latency, jitter: jitter}
}

type latencyPipe struct {
	latency time.Duration
	jitter  time.Duration
}

// Describe implements [vlatte.SelfDescriptive], without a jitter of 0.
func (l latencyPipe) Describe() vlatte.Descriptor {
	if l.jitter == 0 {
		return vlatte.DescribeKeyValuePairs("latency", "type", "latency", "latency", l.latency.String())
	}

	return vlatte.DescribeKeyValuePairs("latency", "type", "latency", "latency", l.latency.String(), "jitter", l.jitter.String())
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

// delay picks the delay of a write or flush: from latency-jitter to latency+jitter, which is at most twice the
// longest duration, so it can't overflow a uint64. One longer than a duration holds is the longest one.
func (l latencyPipe) delay() time.Duration {
	if l.jitter == 0 {
		return l.latency
	}

	d := uint64(l.latency-l.jitter) + rand.Uint64N(2*uint64(l.jitter)+1)

	return time.Duration(min(d, math.MaxInt64))
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
