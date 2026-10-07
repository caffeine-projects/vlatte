package vlatte

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
