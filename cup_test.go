package vlatte_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp"
	"github.com/caffeine-projects/vlatte/drink/xhttp/test/testmock"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/sip"
)

func TestCup(t *testing.T) {
	newTestMock := func() *xhttp.HTTPMock {
		return xhttp.NewMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	repo := vlatte.GrabCanister[*xhttp.HTTPMock]()
	repo.Store(context.Background(), m1, m2, m3)

	cup := vlatte.Pour(repo, []*xhttp.HTTPMock{m1, m2, m3})

	require.Equal(t, 3, len(cup.Beans()))

	b, ok := cup.Bean(m1.ID())
	require.True(t, ok)
	require.Equal(t, m1, b)

	b, ok = cup.Bean("unknown")
	require.False(t, ok)
	require.Nil(t, b)

	t.Run("should not return done when there is still pending mocks", func(t *testing.T) {
		ft := testmock.NewFakeT()

		require.False(t, cup.Called())
		require.Equal(t, 3, len(cup.ListPending()))
		require.True(t, cup.Pending())

		cup.AssertCalled(ft)
		ft.AssertNumberOfCalls(t, "Errorf", 1)
	})

	t.Run("should return done when all mocks were called", func(t *testing.T) {
		ft := testmock.NewFakeT()

		m1.Inc()

		require.False(t, cup.Called())
		cup.AssertCalled(ft)

		m2.Inc()
		m3.Inc()

		ft.AssertNumberOfCalls(t, "Errorf", 1)
		require.True(t, cup.AssertCalled(t))
		require.True(t, cup.Called())
		require.True(t, sip.Times(t, cup, 3))
		require.False(t, sip.Times(ft, cup, 1))
		require.Equal(t, 0, len(cup.ListPending()))
		require.False(t, cup.Pending())
	})

	t.Run("should return total hits from store", func(t *testing.T) {
		require.EqualValues(t, 3, cup.Hits())
		sip.Times(t, cup, 3)
	})

	t.Run("should clean all store associated with NewScope when calling .Clean()", func(t *testing.T) {
		err := cup.Empty(context.Background())
		require.NoError(t, err)

		require.Equal(t, 0, len(cup.ListPending()))
		require.True(t, sip.Times(t, cup, 0))
		require.False(t, cup.Pending())
	})

	t.Run("should only consider enabled store", func(t *testing.T) {
		m := xhttp.New()
		m.MustStart()

		defer m.Close()

		s1 := m.MustBrew(xhttp.Get(filter.URLPath("/test1")).Reply(xhttp.OK()))
		s2 := m.MustBrew(
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

			sip.Times(t, s1, 1)
		})

		t.Run("enabling previously disabled", func(t *testing.T) {
			s1.Enable()

			res, err := http.Get(fmt.Sprintf("%s/test1", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.EqualValues(t, 2, s1.Hits())

			sip.Times(t, s1, 2)
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

	cup := vlatte.Pour(nil, []*xhttp.HTTPMock{m1, m2, m3})

	require.True(t, cup.Toss(m1.ID()))
	require.False(t, cup.Toss("unknown"))

	require.Nil(t, cup.Bean(m1.ID()))
}
