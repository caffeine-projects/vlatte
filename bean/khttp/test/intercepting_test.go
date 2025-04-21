package test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/bean/khttp"
	"github.com/caffeine-projects/vlatte/kcore/flow"
)

type TestInterceptor struct{ txt string }

func (in *TestInterceptor) Handle(p []byte, chain flow.Next) (n int, err error) {
	return chain.Next(append(p, []byte(in.txt)...))
}

func TestInterceptors(t *testing.T) {
	m := khttp.NewAPI(khttp.Setup().LogVerbosity(khttp.LogBody)).CloseWithT(t)
	m.MustStart()

	in1 := &TestInterceptor{"(extra)"}
	in2 := &TestInterceptor{"(other)"}

	scope := m.MustMock(khttp.Getf("/test").Intercept(in1, in2).Reply(khttp.OK().BodyText("hi")))

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
