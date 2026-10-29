package vlatte

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	_ Stage = (*partsSink)(nil)
	_ Stage = (*countStage)(nil)
	_ Stage = (*logStage)(nil)
	_ Stage = (*holdStage)(nil)
	_ Stage = (*shiftStage)(nil)
	_ Stage = (*timedSink)(nil)
	_ Stage = failStage{}
	_ Stage = lateStage{}
	_ Pipe  = latePipe{}
	_ Pipe  = (*TestPipe)(nil)
	_ Pipe  = countPipe{}
	_ Pipe  = logPipe{}
	_ Pipe  = holdPipe{}
	_ Pipe  = shiftPipe{}
	_ Pipe  = abortAfter{}
)

// TestPipe appends "--" and its text to every buffer it passes on.
type TestPipe struct{ txt string }

func (in *TestPipe) Open(flow *Flow, next Stage) Stage {
	return PipeFunc(func(_ context.Context, p []byte, next Stage) (int, error) {
		if _, err := next.Write(append(p, []byte("--"+in.txt)...)); err != nil {
			return 0, err
		}

		return len(p), nil
	}).Open(flow, next)
}

type TestWriter struct {
	mock.Mock
}

func (m *TestWriter) Write(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func TestFlow(t *testing.T) {
	ctx := context.Background()
	data := []byte("init")
	expected := []byte("init--hello--world!")
	pipe1 := &TestPipe{"hello"}
	pipe2 := &TestPipe{"world"}
	pipe3 := PipeFunc(func(_ context.Context, p []byte, next Stage) (n int, err error) {
		if _, err := next.Write(append(p, []byte("!")...)); err != nil {
			return 0, err
		}

		return len(p), nil
	})

	w := &TestWriter{}
	w.On("Write", expected).Return(len(expected), nil)
	flow := NewFlow(ctx, &Sink{w}, []Pipe{pipe1, pipe2, pipe3})

	n, err := flow.Write(data)

	require.NoError(t, err)
	require.Equal(t, len(data), n)
	require.True(t, w.AssertCalled(t, "Write", expected))
}

func TestFlow_LeavesThePipesAlone(t *testing.T) {
	// D1: a mock's pipes are shared by its requests. Flow appended its sink to them, so with spare capacity a second
	// pipeline's sink replaced the first one's, and one request wrote into another's response.
	pipes := make([]Pipe, 1, 2)
	pipes[0] = PipeFunc(passOn)

	var first, second bytes.Buffer
	a := NewFlow(t.Context(), &Sink{W: &first}, pipes)
	b := NewFlow(t.Context(), &Sink{W: &second}, pipes)

	_, err := a.Write([]byte("a"))
	require.NoError(t, err)
	_, err = b.Write([]byte("b"))
	require.NoError(t, err)

	require.Equal(t, "a", first.String())
	require.Equal(t, "b", second.String())
}

func passOn(_ context.Context, b []byte, next Stage) (int, error) { return next.Write(b) }

// slicePipe passes its buffer on in parts of size bytes, as fault.Slicer does. It slices the buffer without
// clipping the parts, as a hand-written pipe may.
func slicePipe(size int) Pipe {
	return PipeFunc(func(_ context.Context, p []byte, next Stage) (int, error) {
		for i := 0; i < len(p); i += size {
			if _, err := next.Write(p[i:min(i+size, len(p))]); err != nil {
				return i, err
			}
		}

		return len(p), nil
	})
}

// partsSink is a sink that keeps every buffer it gets.
type partsSink struct{ parts []string }

func (s *partsSink) Write(p []byte) (int, error) {
	s.parts = append(s.parts, string(p))
	return len(p), nil
}

func (s *partsSink) Flush() error { return nil }

func (s *partsSink) Close() error { return nil }

func TestFlow_APipeCanPassItsBufferInParts(t *testing.T) {
	// fault.Slicer and fault.Bandwidth pass one buffer on in parts. The flow kept one position for all its calls, so
	// once the first part reached the sink, the second got io.EOF.
	sink := &partsSink{}

	n, err := NewFlow(t.Context(), sink, []Pipe{slicePipe(2)}).Write([]byte("abcde"))

	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, []string{"ab", "cd", "e"}, sink.parts)
}

func TestFlow_APipeThatDropsEverythingDropsEveryPart(t *testing.T) {
	// A pipe that passes nothing on, as fault.Timeout does, drops every part a pipe before it passes. The flow
	// resumed a part from the pipe after the last one that ran, so the second part got past the drop.
	drop := PipeFunc(func(_ context.Context, p []byte, _ Stage) (int, error) { return len(p), nil })

	var out bytes.Buffer
	n, err := NewFlow(t.Context(), &Sink{W: &out}, []Pipe{slicePipe(2), drop}).Write([]byte("abcd"))

	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Empty(t, out.String())
}

func TestFlow_AppendingToAPartLeavesTheNextPartAlone(t *testing.T) {
	// A part shares its array with the parts after it. A pipe that appended to a part would write over the next one
	// before it was passed on.
	b := make([]byte, 4, 16)
	copy(b, "abcd")
	sink := &partsSink{}

	_, err := NewFlow(t.Context(), sink, []Pipe{slicePipe(2), &TestPipe{"x"}}).Write(b)

	require.NoError(t, err)
	require.Equal(t, []string{"ab--x", "cd--x"}, sink.parts)
	require.Equal(t, "abcd", string(b[:4]))
}

func TestFlow_APipeThatFailsStopsTheFlow(t *testing.T) {
	// A pipe that fails fails the write: its error goes back through the pipes before it, and the pipes after it and
	// the sink get nothing.
	errFailed := errors.New("pipe: failed")
	fail := PipeFunc(func(context.Context, []byte, Stage) (int, error) { return 0, errFailed })

	var out bytes.Buffer
	flow := NewFlow(t.Context(), &Sink{W: &out}, []Pipe{&TestPipe{"before"}, fail, &TestPipe{"after"}})
	n, err := flow.Write([]byte("a"))

	require.ErrorIs(t, err, errFailed)
	require.Zero(t, n)
	require.Empty(t, out.String())

	// An error that doesn't ask for an abort leaves the flow as it was: its caller decides.
	require.NoError(t, flow.Context().Err())
}

type ctxKey struct{}

func TestFlow_EveryPipeGetsTheContextOfTheFlow(t *testing.T) {
	// A pipe stops when the client leaves, as fault.Timeout must, through the context the flow was given.
	ctx := context.WithValue(t.Context(), ctxKey{}, "request")

	var got []any
	record := PipeFunc(func(ctx context.Context, p []byte, next Stage) (int, error) {
		got = append(got, ctx.Value(ctxKey{}))
		return next.Write(p)
	})

	_, err := NewFlow(ctx, &partsSink{}, []Pipe{record, record}).Write([]byte("a"))

	require.NoError(t, err)
	require.Equal(t, []any{"request", "request"}, got)
}

func TestFlow_AFlowWhoseContextEndedTakesNothingMore(t *testing.T) {
	// The client left: the flow's context ends with the request's, and what the reply still writes goes nowhere.
	ctx, cancel := context.WithCancel(t.Context())
	sink := &partsSink{}
	flow := NewFlow(ctx, sink, nil)

	cancel()

	_, err := flow.Write([]byte("a"))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, flow.Flush(), context.Canceled)
	require.Empty(t, sink.parts)
}

func TestSink_WriteReturnsWhatItsWriterReturns(t *testing.T) {
	// A Sink used as an io.Writer writes to W, and reports what W reports, a short write included.
	errShort := errors.New("writer: short")
	w := &TestWriter{}
	w.On("Write", []byte("ab")).Return(1, errShort)

	var sink io.Writer = &Sink{W: w}
	n, err := sink.Write([]byte("ab"))

	require.ErrorIs(t, err, errShort)
	require.Equal(t, 1, n)
	w.AssertExpectations(t)
}

func TestSink_FlushFlushesAWriterThatCan(t *testing.T) {
	// A buffered writer holds what it got until it is flushed: a flush of the flow reaches it.
	var out bytes.Buffer
	buffered := bufio.NewWriter(&out)
	flow := NewFlow(t.Context(), &Sink{W: buffered}, nil)

	_, err := flow.Write([]byte("a"))
	require.NoError(t, err)
	require.Empty(t, out.String())

	require.NoError(t, flow.Flush())
	require.Equal(t, "a", out.String())

	// A writer that can't flush, and the end of the flow, leave W as it is.
	require.NoError(t, (&Sink{W: &out}).Flush())
	require.NoError(t, flow.Close())
	require.Equal(t, "a", out.String())
}

// countPipe counts the bytes its stage passed, in that stage, and sends the count at the end.
type countPipe struct{ counts chan<- int }

func (s countPipe) Open(_ *Flow, next Stage) Stage {
	return &countStage{counts: s.counts, next: next}
}

type countStage struct {
	counts chan<- int
	next   Stage
	n      int
}

func (s *countStage) Write(b []byte) (int, error) {
	s.n += len(b)
	return s.next.Write(b)
}

func (s *countStage) Flush() error { return s.next.Flush() }

func (s *countStage) Close() error {
	s.counts <- s.n
	return s.next.Close()
}

func TestFlow_EachFlowHasItsOwnStageOfAPipe(t *testing.T) {
	// A limit on the bytes of a response counts that response's only, though the mock's pipes serve all of them.
	counts := make(chan int, 2)
	pipe := countPipe{counts: counts}

	a := NewFlow(t.Context(), &partsSink{}, []Pipe{pipe})
	b := NewFlow(t.Context(), &partsSink{}, []Pipe{pipe})

	_, err := a.Write([]byte("aaa"))
	require.NoError(t, err)
	_, err = b.Write([]byte("b"))
	require.NoError(t, err)

	require.NoError(t, a.Close())
	require.NoError(t, b.Close())
	require.Equal(t, 3, <-counts)
	require.Equal(t, 1, <-counts)
}

// logPipe logs each call its stage gets, by name, before it passes the call on.
type logPipe struct {
	name string
	log  *[]string
}

func (s logPipe) Open(_ *Flow, next Stage) Stage {
	return &logStage{name: s.name, log: s.log, next: next}
}

type logStage struct {
	name string
	log  *[]string
	next Stage
}

func (s *logStage) Write(b []byte) (int, error) {
	*s.log = append(*s.log, s.name+" write "+string(b))
	return s.next.Write(b)
}

func (s *logStage) Flush() error {
	*s.log = append(*s.log, s.name+" flush")
	return s.next.Flush()
}

func (s *logStage) Close() error {
	*s.log = append(*s.log, s.name+" close")
	return s.next.Close()
}

func TestFlow_FlushAndCloseReachEveryStageThenTheSink(t *testing.T) {
	// A flush of a stream reaches the client only once every stage passed it on, and so does its end.
	var log []string
	sink := &logStage{name: "sink", log: &log, next: &partsSink{}}
	pipes := []Pipe{logPipe{name: "a", log: &log}, PipeFunc(passOn), logPipe{name: "b", log: &log}}
	flow := NewFlow(t.Context(), sink, pipes)

	_, err := flow.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, flow.Flush())
	require.NoError(t, flow.Close())

	require.Equal(t, []string{
		"a write x", "b write x", "sink write x",
		"a flush", "b flush", "sink flush",
		"a close", "b close", "sink close",
	}, log)
}

// holdPipe holds what its stage gets until a flush or the end, as a stage that sends a body in parts of its own
// does.
type holdPipe struct{}

func (holdPipe) Open(_ *Flow, next Stage) Stage { return &holdStage{next: next} }

type holdStage struct {
	held []byte
	next Stage
}

func (s *holdStage) Write(b []byte) (int, error) {
	s.held = append(s.held, b...)
	return len(b), nil
}

func (s *holdStage) Flush() error {
	if err := s.release(); err != nil {
		return err
	}

	return s.next.Flush()
}

func (s *holdStage) Close() error {
	if err := s.release(); err != nil {
		return err
	}

	return s.next.Close()
}

func (s *holdStage) release() error {
	if len(s.held) == 0 {
		return nil
	}

	_, err := s.next.Write(s.held)
	s.held = nil

	return err
}

func TestFlow_WhatAStageHoldsGoesOnAtAFlushAndAtTheEnd(t *testing.T) {
	// The end of a body is an event of its own: a stage that holds bytes, such as one that sends a body in timed
	// parts, still gets to send them.
	sink := &partsSink{}
	flow := NewFlow(t.Context(), sink, []Pipe{holdPipe{}})

	_, err := flow.Write([]byte("a"))
	require.NoError(t, err)
	_, err = flow.Write([]byte("b"))
	require.NoError(t, err)
	require.Empty(t, sink.parts)

	require.NoError(t, flow.Flush())
	require.Equal(t, []string{"ab"}, sink.parts)

	_, err = flow.Write([]byte("c"))
	require.NoError(t, err)
	require.NoError(t, flow.Close())
	require.Equal(t, []string{"ab", "c"}, sink.parts)

	// The stream ended: its stages ran their end once, and a write goes nowhere.
	require.NoError(t, flow.Close())
	_, err = flow.Write([]byte("d"))
	require.ErrorIs(t, err, errClosed)
	require.Equal(t, []string{"ab", "c"}, sink.parts)
}

func TestFlow_AbortEndsTheFlowWithItsCause(t *testing.T) {
	// A stage's timer aborts the flow from another goroutine: the reply that waits on the flow's context stops, and
	// the server knows to cut the connection, or to reset it.
	errIdle := errors.New("pipe: idle")

	tests := []struct {
		name  string
		err   error
		reset bool
	}{
		{name: "an error of its own", err: errIdle},
		{name: "no error", err: nil},
		{name: "a reset", err: ErrReset, reset: true},
		{name: "an abort", err: ErrAbort},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &partsSink{}
			flow := NewFlow(t.Context(), sink, nil)

			var wg sync.WaitGroup
			wg.Go(func() { flow.Abort(tc.err) })
			<-flow.Context().Done()
			wg.Wait()

			cause := context.Cause(flow.Context())
			require.ErrorIs(t, cause, ErrAbort)
			if tc.err != nil {
				require.ErrorIs(t, cause, tc.err)
			}
			require.Equal(t, tc.reset, errors.Is(cause, ErrReset))

			_, err := flow.Write([]byte("a"))
			require.ErrorIs(t, err, ErrAbort)
			require.Empty(t, sink.parts)

			// The first end wins.
			flow.Abort(errors.New("pipe: later"))
			require.NoError(t, flow.Close())
			require.Equal(t, cause, context.Cause(flow.Context()))
		})
	}
}

// failStage fails every call with err.
type failStage struct{ err error }

func (s failStage) Write([]byte) (int, error) { return 0, s.err }

func (s failStage) Flush() error { return s.err }

func (s failStage) Close() error { return s.err }

func TestFlow_AStageThatReturnsAnAbortAbortsTheFlow(t *testing.T) {
	// A limit on the bytes of a response returns a reset once it is reached: the server learns it from the flow's
	// context, as when a stage calls Abort, whatever the reply does with the error.
	tests := []struct {
		name string
		call func(flow *Flow) error
	}{
		{name: "write", call: func(flow *Flow) error { _, err := flow.Write([]byte("a")); return err }},
		{name: "flush", call: (*Flow).Flush},
		{name: "close", call: (*Flow).Close},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flow := NewFlow(t.Context(), failStage{err: ErrReset}, nil)

			require.ErrorIs(t, tc.call(flow), ErrReset)

			cause := context.Cause(flow.Context())
			require.ErrorIs(t, cause, ErrAbort)
			require.ErrorIs(t, cause, ErrReset)
		})
	}
}

// latePipe passes something on at its end, whatever happened before, as a stage that ignores the end of its flow does.
type latePipe struct{}

func (latePipe) Open(_ *Flow, next Stage) Stage { return lateStage{next: next} }

type lateStage struct{ next Stage }

func (s lateStage) Write(b []byte) (int, error) { return s.next.Write(b) }

func (s lateStage) Flush() error { return s.next.Flush() }

func (s lateStage) Close() error {
	_, _ = s.next.Write([]byte("late"))
	_ = s.next.Flush()

	return s.next.Close()
}

func TestFlow_NothingReachesTheSinkOnceTheFlowEnded(t *testing.T) {
	// An abort cuts the stream there: a stage that still passes something on, as its end runs, doesn't send it after
	// the cut, such as a second answer on a connection that was cut.
	var log []string
	sink := &logStage{name: "sink", log: &log, next: &partsSink{}}
	flow := NewFlow(t.Context(), sink, []Pipe{latePipe{}})

	flow.Abort(nil)

	require.NoError(t, flow.Close())
	require.Equal(t, []string{"sink close"}, log)
}

func TestFlow_CloseEndsTheContext(t *testing.T) {
	// A stage's timers and goroutines end with the flow's context: once the stream ended, nothing of it runs on.
	flow := NewFlow(t.Context(), &partsSink{}, nil)

	require.NoError(t, flow.Close())

	require.ErrorIs(t, context.Cause(flow.Context()), errClosed)
	require.NotErrorIs(t, context.Cause(flow.Context()), ErrAbort)
}

// abortAfter aborts its flow d after the flow opened, with err, as fault.Timeout does on a stream with no data. It
// passes everything on.
type abortAfter struct {
	d   time.Duration
	err error
}

func (a abortAfter) Open(flow *Flow, next Stage) Stage {
	timer := time.AfterFunc(a.d, func() { flow.Abort(a.err) })
	context.AfterFunc(flow.Context(), func() { timer.Stop() })

	return next
}

func TestFlow_AStageCanAbortTheFlowOnATimer(t *testing.T) {
	// A reply waiting for its target writes nothing, so a stage that only ran on writes couldn't time it out.
	synctest.Test(t, func(t *testing.T) {
		errIdle := errors.New("pipe: idle")
		start := time.Now()

		flow := NewFlow(t.Context(), &partsSink{}, []Pipe{abortAfter{d: time.Second, err: errIdle}})
		<-flow.Context().Done()

		require.Equal(t, time.Second, time.Since(start))
		require.ErrorIs(t, context.Cause(flow.Context()), ErrAbort)
		require.ErrorIs(t, context.Cause(flow.Context()), errIdle)
	})
}

// shiftPipe passes every write on d after it came, from a goroutine of its stage, as fault.Latency does: the writer
// goes on meanwhile, so the whole stream is shifted by d, not slowed down.
type shiftPipe struct{ d time.Duration }

func (s shiftPipe) Open(flow *Flow, next Stage) Stage {
	st := &shiftStage{
		ctx:   flow.Context(),
		d:     s.d,
		next:  next,
		queue: make(chan shifted, 16),
		done:  make(chan error, 1),
	}
	go st.run()

	return st
}

type shifted struct {
	b   []byte
	due time.Time
}

type shiftStage struct {
	ctx   context.Context
	d     time.Duration
	next  Stage
	queue chan shifted
	done  chan error
}

func (s *shiftStage) Write(b []byte) (int, error) {
	select {
	case s.queue <- shifted{b: bytes.Clone(b), due: time.Now().Add(s.d)}:
		return len(b), nil
	case <-s.ctx.Done():
		return 0, context.Cause(s.ctx)
	}
}

func (s *shiftStage) Flush() error { return nil }

func (s *shiftStage) Close() error {
	close(s.queue)

	if err := <-s.done; err != nil {
		return err
	}

	return s.next.Close()
}

func (s *shiftStage) run() {
	for {
		select {
		case c, ok := <-s.queue:
			if !ok {
				s.done <- nil
				return
			}

			if err := s.pass(c); err != nil {
				s.done <- err
				return
			}
		case <-s.ctx.Done():
			s.done <- context.Cause(s.ctx)
			return
		}
	}
}

func (s *shiftStage) pass(c shifted) error {
	select {
	case <-time.After(time.Until(c.due)):
		_, err := s.next.Write(c.b)
		return err
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	}
}

// timedSink keeps every buffer it gets with the time it came, from start.
type timedSink struct {
	start time.Time
	got   []string
}

func (s *timedSink) Write(b []byte) (int, error) {
	s.got = append(s.got, string(b)+" at "+time.Since(s.start).String())
	return len(b), nil
}

func (s *timedSink) Flush() error { return nil }

func (s *timedSink) Close() error { return nil }

func TestFlow_AStageCanShiftAStreamInTime(t *testing.T) {
	// Latency shifts a stream: each part arrives d after it was sent, however close the parts are. A stage that
	// blocked each write for d instead would have the second part arrive 2d after the first was sent.
	synctest.Test(t, func(t *testing.T) {
		sink := &timedSink{start: time.Now()}
		flow := NewFlow(t.Context(), sink, []Pipe{shiftPipe{d: 200 * time.Millisecond}})

		_, err := flow.Write([]byte("a"))
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond)

		_, err = flow.Write([]byte("b"))
		require.NoError(t, err)

		// Close returns once the stage passed on all it held: nothing reaches the sink after.
		require.NoError(t, flow.Close())
		require.Equal(t, []string{"a at 200ms", "b at 250ms"}, sink.got)
	})
}
