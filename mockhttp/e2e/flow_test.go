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

type TestStep struct{ txt string }

func (in *TestStep) Handle(_ context.Context, p []byte, next vlatte.Next) (n int, err error) {
	return next.Next(append(p, []byte(in.txt)...))
}

func TestFlow(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().LogVerbosity(vlatte.LogBody))
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	in1 := &TestStep{"(extra)"}
	in2 := &TestStep{"(other)"}

	scope := m.MustMock(mockhttp.Getf("/test").Step(in1, in2).Reply(mockhttp.OK().BodyText("hi")))

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
