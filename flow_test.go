package vlatte

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type TestStep struct{ txt string }

func (in *TestStep) Handle(p []byte, next Next) (n int, err error) {
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
	data := []byte("init")
	expected := []byte("init--hello--world!")
	step1 := &TestStep{"hello"}
	step2 := &TestStep{"world"}
	step3 := Func(func(p []byte, next Next) (n int, err error) {
		return next.Next(append(p, []byte("!")...))
	})

	w := &TestWriter{}
	w.On("Write", expected).Return(len(expected), nil)
	pipeline := Flow(&Sink{w}, []Step{step1, step2, step3})

	n, err := pipeline.Next(data)

	require.NoError(t, err)
	require.Equal(t, len(expected), n)
	require.True(t, w.AssertCalled(t, "Write", expected))
}
