package e2e

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

type TestPipe struct{ txt string }

func (in *TestPipe) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return vlatte.PipeFunc(func(_ context.Context, p []byte, next vlatte.Stage) (int, error) {
		if _, err := next.Write(append(p, []byte(in.txt)...)); err != nil {
			return 0, err
		}

		return len(p), nil
	}).Open(flow, next)
}

func TestFlow(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().LogVerbosity(vlatte.LogBody))
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	in1 := &TestPipe{"(extra)"}
	in2 := &TestPipe{"(other)"}

	scope := m.MustMock(mockhttp.Getf("/test").Pipe(in1, in2).Reply(mockhttp.OK().BodyText("hi")))

	client := &http.Client{}
	res, err := client.Get(m.URL("/test"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scope.AssertCalled(t))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hi(extra)(other)", string(b))
}
