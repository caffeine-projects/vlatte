package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestScope_OnlyEnabledMocksMatch(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	s1 := m.MustMock(mockhttp.Get(match.URLPath("/test1")).Reply(mockhttp.OK()))
	s2 := m.MustMock(
		mockhttp.Get(match.URLPath("/test2")).Reply(mockhttp.OK()),
		mockhttp.Get(match.URLPath("/test3")).Reply(mockhttp.OK()))

	t.Run("initial state (enabled)", func(t *testing.T) {
		res, err := http.Get(fmt.Sprintf("%s/test1", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err = http.Get(fmt.Sprintf("%s/test2", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.True(t, s1.Called())
		require.True(t, s2.Called())
	})

	t.Run("disabled", func(t *testing.T) {
		s1.Disable()

		res, err := http.Get(fmt.Sprintf("%s/test1", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

		res, err = http.Get(fmt.Sprintf("%s/test2", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, int64(1), s1.Hits())

		vlatte.Times(t, s1, 1)
	})

	t.Run("enabling previously disabled", func(t *testing.T) {
		s1.Enable()

		res, err := http.Get(fmt.Sprintf("%s/test1", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.EqualValues(t, 2, s1.Hits())

		vlatte.Times(t, s1, 2)
	})

	t.Run("disabling multiple", func(t *testing.T) {
		s2.Disable()

		res, err := http.Get(fmt.Sprintf("%s/test2", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

		res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	})
}
