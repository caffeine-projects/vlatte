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

// seen is what a callback saw, sent to the test's goroutine: a require in a callback runs on another one.
type seen struct {
	kind   string
	mockID string
	method string
	path   string
	body   any
	status int
}

// see returns a callback that sends what it sees on the channel it returns, and then returns err.
func see(err error) (vlatte.EventFunc[vlatte.OnComplete], <-chan seen) {
	ch := make(chan seen, 1)

	return func(_ context.Context, e vlatte.OnComplete) error {
		in, ok := e.(*mockhttp.OnComplete)
		if !ok {
			ch <- seen{kind: fmt.Sprintf("%T", e)}
			return err
		}

		ch <- seen{
			kind:   in.Kind().String(),
			mockID: in.Mock().ID(),
			method: in.Request().RawRequest.Method,
			path:   in.Request().URL.Path,
			body:   in.Request().ParsedBodyData,
			status: in.Response().StatusCode,
		}

		return err
	}, ch
}

// received returns what ch got, which the response waits for.
func received(t *testing.T, ch <-chan seen) seen {
	t.Helper()

	select {
	case s := <-ch:
		return s
	default:
		t.Fatal("the response came before the callback ran")
		return seen{}
	}
}

func TestCallbacks(t *testing.T) {
	t.Run("should call registered post action", func(t *testing.T) {
		m := mockhttp.MustNew(mockhttp.Setup().Silent())
		m.MustStart()

		defer m.Close()

		callback, ch := see(nil)
		scope := m.MustMock(mockhttp.Post(match.URLPath("/test")).Callback(callback).Reply(mockhttp.OK()))

		res, err := http.Post(fmt.Sprintf("%s/test", m.URL()), mockhttp.MIMETextPlain, strings.NewReader("hi"))
		require.NoError(t, err)

		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.True(t, scope.AssertCalled(t))
		require.Equal(t, seen{
			kind:   "http",
			mockID: scope.IDs()[0],
			method: http.MethodPost,
			path:   "/test",
			body:   "hi",
			status: http.StatusOK,
		}, received(t, ch))
	})

	t.Run("should not be affected by errors on registered post actions", func(t *testing.T) {
		m := mockhttp.MustNew(mockhttp.Setup().Silent())
		m.MustStart()

		defer m.Close()

		callback, ch := see(errors.New("failed to run post action"))
		scope := m.MustMock(mockhttp.Get(match.URLPath("/test")).Callback(callback).Reply(mockhttp.OK()))

		res, err := http.Get(fmt.Sprintf("%s/test", m.URL()))
		require.NoError(t, err)

		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.True(t, scope.AssertCalled(t))
		require.Equal(t, scope.IDs()[0], received(t, ch).mockID)
	})
}
