package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestCallbacks(t *testing.T) {
	t.Run("should call registered post action", func(t *testing.T) {
		spy := false
		m := mockhttp.MustNew()
		m.MustStart()

		defer m.Close()

		scope := m.MustMock(mockhttp.Post(match.URLPath("/test")).
			Callback(func(ctx context.Context, input vlatte.CallbackInput) error {
				cinput, ok := input.(*mockhttp.CallbackInput)

				require.True(t, ok)
				require.Equal(t, "http", cinput.Kind().String())
				require.NotNil(t, cinput)
				require.NotNil(t, cinput.Mock())
				require.NotNil(t, cinput.Request())
				require.NotNil(t, cinput.Response())
				require.Equal(t, http.MethodPost, cinput.Request().RawRequest.Method)
				require.Equal(t, "/test", cinput.Request().URL.Path)
				require.Equal(t, "hi", cinput.Request().ParsedBodyData.(string))

				spy = true

				return nil
			}).
			Reply(mockhttp.OK()))

		res, err := http.Post(fmt.Sprintf("%s/test", m.URL()), mockhttp.MIMETextPlain, strings.NewReader("hi"))
		require.NoError(t, err)

		defer res.Body.Close()

		require.True(t, scope.AssertCalled(t))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.True(t, spy)
	})

	t.Run("should not be affected by errors on registered post actions", func(t *testing.T) {
		var callbackErrReceiver error

		callbackErr := errors.New("failed to run post action")
		m := mockhttp.MustNew()
		m.MustStart()

		defer m.Close()

		scope := m.MustMock(mockhttp.Get(match.URLPath("/test")).
			Callback(func(ctx context.Context, input vlatte.CallbackInput) error {
				cinput, ok := input.(*mockhttp.CallbackInput)
				require.True(t, ok)
				require.NotNil(t, cinput)

				callbackErrReceiver = callbackErr

				return callbackErr
			}).
			Reply(mockhttp.OK()))

		res, err := http.Get(fmt.Sprintf("%s/test", m.URL()))
		require.NoError(t, err)

		defer res.Body.Close()

		require.True(t, scope.AssertCalled(t))
		require.NotNil(t, callbackErrReceiver)
		require.Equal(t, callbackErr, callbackErrReceiver)
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}
