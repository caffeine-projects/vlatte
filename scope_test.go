package vlatte_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

func TestScope(t *testing.T) {
	newTestMock := func() *xhttp.HTTPMock {
		return xhttp.NewMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	repo := vlatte.NewStore[*xhttp.HTTPMock]()
	repo.Store(context.Background(), m1, m2, m3)

	scope := vlatte.NewScope([]*xhttp.HTTPMock{m1, m2, m3}, repo)

	require.Equal(t, 3, len(scope.Mocks()))

	require.Equal(t, m1, scope.Mock(m1.ID()))
	require.Nil(t, scope.Mock("unknown"))

	t.Run("should not return done when there is still pending mocks", func(t *testing.T) {
		ft := testmock.NewFakeT()

		require.False(t, scope.Called())
		require.Equal(t, 3, len(scope.ListPending()))
		require.True(t, scope.Pending())

		scope.AssertCalled(ft)
		ft.AssertNumberOfCalls(t, "Errorf", 1)
	})

	t.Run("should return done when all mocks were called", func(t *testing.T) {
		ft := testmock.NewFakeT()

		m1.Inc()

		require.False(t, scope.Called())
		scope.AssertCalled(ft)

		m2.Inc()
		m3.Inc()

		ft.AssertNumberOfCalls(t, "Errorf", 1)
		require.True(t, scope.AssertCalled(t))
		require.True(t, scope.Called())
		require.True(t, vlatte.Times(t, scope, 3))
		require.False(t, vlatte.Times(ft, scope, 1))
		require.Equal(t, 0, len(scope.ListPending()))
		require.False(t, scope.Pending())
	})

	t.Run("should return total hits from store", func(t *testing.T) {
		require.EqualValues(t, 3, scope.Hits())
		vlatte.Times(t, scope, 3)
	})

	t.Run("should clean all store associated with NewScope when calling .Clear()", func(t *testing.T) {
		err := scope.Clear(context.Background())
		require.NoError(t, err)

		require.Equal(t, 0, len(scope.ListPending()))
		require.True(t, vlatte.Times(t, scope, 0))
		require.False(t, scope.Pending())
	})

	t.Run("should only consider enabled store", func(t *testing.T) {
		m := xhttp.New()
		m.MustStart()

		defer m.Close()

		s1 := m.MustMock(xhttp.Get(filter.URLPath("/test1")).Reply(xhttp.OK()))
		s2 := m.MustMock(
			xhttp.Get(filter.URLPath("/test2")).Reply(xhttp.OK()),
			xhttp.Get(filter.URLPath("/test3")).Reply(xhttp.OK()))

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
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)

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
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)

			res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)
		})
	})
}

func TestScopedDelete(t *testing.T) {
	newTestMock := func() *xhttp.HTTPMock {
		return xhttp.NewMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	store := vlatte.NewStore[*xhttp.HTTPMock]()
	err := store.Store(t.Context(), m1, m2, m3)
	require.NoError(t, err)

	scope := vlatte.NewScope([]*xhttp.HTTPMock{m1, m2, m3}, store)

	require.NoError(t, scope.Delete(t.Context(), m1.ID()))
	require.NoError(t, scope.Delete(t.Context(), "unknown"))

	require.Nil(t, scope.Mock(m1.ID()))
}
