package vlatte

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var _ Step = (*partsSink)(nil)

type TestStep struct{ txt string }

func (in *TestStep) Handle(_ context.Context, p []byte, next Next) (n int, err error) {
	return next.Next(append(p, []byte("--"+in.txt)...))
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
	step1 := &TestStep{"hello"}
	step2 := &TestStep{"world"}
	step3 := StepFunc(func(_ context.Context, p []byte, next Next) (n int, err error) {
		return next.Next(append(p, []byte("!")...))
	})

	w := &TestWriter{}
	w.On("Write", expected).Return(len(expected), nil)
	pipeline := Flow(ctx, &Sink{w}, []Step{step1, step2, step3})

	n, err := pipeline.Next(data)

	require.NoError(t, err)
	require.Equal(t, len(expected), n)
	require.True(t, w.AssertCalled(t, "Write", expected))
}

func TestFlow_LeavesTheStepsAlone(t *testing.T) {
	// D1: a mock's steps are shared by its requests. Flow appended its sink to them, so with spare capacity a second
	// pipeline's sink replaced the first one's, and one request wrote into another's response.
	steps := make([]Step, 1, 2)
	steps[0] = StepFunc(func(_ context.Context, p []byte, next Next) (int, error) { return next.Next(p) })

	var first, second bytes.Buffer
	a := Flow(t.Context(), &Sink{W: &first}, steps)
	b := Flow(t.Context(), &Sink{W: &second}, steps)

	_, err := a.Next([]byte("a"))
	require.NoError(t, err)
	_, err = b.Next([]byte("b"))
	require.NoError(t, err)

	require.Equal(t, "a", first.String())
	require.Equal(t, "b", second.String())
}

// sliceStep passes its buffer on in parts of size bytes, as the slicer toxic does, and returns the sum of what the
// rest of the flow returned. It slices the buffer without clipping the parts, as a hand-written step may.
func sliceStep(size int) Step {
	return StepFunc(func(_ context.Context, p []byte, next Next) (int, error) {
		var n int
		for i := 0; i < len(p); i += size {
			m, err := next.Next(p[i:min(i+size, len(p))])
			n += m
			if err != nil {
				return n, err
			}
		}

		return n, nil
	})
}

// partsSink is a sink that keeps every buffer it gets.
type partsSink struct{ parts []string }

func (s *partsSink) Handle(_ context.Context, p []byte, _ Next) (int, error) {
	s.parts = append(s.parts, string(p))
	return len(p), nil
}

func TestFlow_AStepCanPassItsBufferInParts(t *testing.T) {
	// The slicer and bandwidth toxics pass one buffer on in parts. The flow kept one position for all its calls, so
	// once the first part reached the sink, the second got io.EOF.
	sink := &partsSink{}

	n, err := Flow(t.Context(), sink, []Step{sliceStep(2)}).Next([]byte("abcde"))

	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, []string{"ab", "cd", "e"}, sink.parts)
}

func TestFlow_AStepThatDropsEverythingDropsEveryPart(t *testing.T) {
	// A step that passes nothing on, as the timeout toxic does, drops every part a step before it passes. The flow
	// resumed a part from the step after the last one that ran, so the second part got past the drop.
	drop := StepFunc(func(_ context.Context, p []byte, _ Next) (int, error) { return len(p), nil })

	var out bytes.Buffer
	n, err := Flow(t.Context(), &Sink{W: &out}, []Step{sliceStep(2), drop}).Next([]byte("abcd"))

	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Empty(t, out.String())
}

func TestFlow_AppendingToAPartLeavesTheNextPartAlone(t *testing.T) {
	// A part shares its array with the parts after it. A step that appended to a part would write over the next one
	// before it was passed on.
	b := make([]byte, 4, 16)
	copy(b, "abcd")
	sink := &partsSink{}

	_, err := Flow(t.Context(), sink, []Step{sliceStep(2), &TestStep{"x"}}).Next(b)

	require.NoError(t, err)
	require.Equal(t, []string{"ab--x", "cd--x"}, sink.parts)
	require.Equal(t, "abcd", string(b[:4]))
}

func TestFlow_AStepThatFailsStopsTheFlow(t *testing.T) {
	// A step that fails, as the reset_peer toxic does, fails the write: its error goes back through the steps before
	// it, and the steps after it and the sink get nothing.
	errReset := errors.New("step: reset")
	fail := StepFunc(func(context.Context, []byte, Next) (int, error) { return 0, errReset })

	var out bytes.Buffer
	n, err := Flow(t.Context(), &Sink{W: &out}, []Step{&TestStep{"before"}, fail, &TestStep{"after"}}).Next([]byte("a"))

	require.ErrorIs(t, err, errReset)
	require.Zero(t, n)
	require.Empty(t, out.String())
}

func TestFlow_EveryStepGetsTheContextOfTheFlow(t *testing.T) {
	// A step stops when the client leaves, as the timeout toxic must, through the context the flow was given. The
	// sink gets it too.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var errs []error
	record := StepFunc(func(ctx context.Context, p []byte, next Next) (int, error) {
		errs = append(errs, ctx.Err())
		return next.Next(p)
	})
	sink := StepFunc(func(ctx context.Context, p []byte, _ Next) (int, error) {
		errs = append(errs, ctx.Err())
		return len(p), nil
	})

	_, err := Flow(ctx, sink, []Step{record, record}).Next([]byte("a"))

	require.NoError(t, err)
	require.Equal(t, []error{context.Canceled, context.Canceled, context.Canceled}, errs)
}

func TestFlow_NothingFollowsTheSink(t *testing.T) {
	// A sink that passes its buffer on learns that the flow ends there.
	sink := StepFunc(func(_ context.Context, p []byte, next Next) (int, error) { return next.Next(p) })

	n, err := Flow(t.Context(), sink, nil).Next([]byte("a"))

	require.ErrorIs(t, err, io.EOF)
	require.Zero(t, n)
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
