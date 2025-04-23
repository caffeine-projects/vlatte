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

func TestMug(t *testing.T) {

	newTestMock := func() *xhttp.HTTPMock {
		return xhttp.NewMock()
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	repo := vlatte.GrabCanister[*xhttp.HTTPMock]()
	repo.Store(context.Background(), m1, m2, m3)

	mug := vlatte.Pour(repo, []*xhttp.HTTPMock{m1, m2, m3})

	require.Equal(t, 3, len(mug.Beans()))
	require.Equal(t, m1, mug.Bean(m1.ID()))
	require.Nil(t, mug.Bean("unknown"))

	t.Run("should not return done when there is still pending mocks", func(t *testing.T) {
		ft := testmock.NewFakeT()

		require.False(t, mug.Called())
		require.Equal(t, 3, len(mug.ListPending()))
		require.True(t, mug.Pending())

		mug.AssertCalled(ft)
		ft.AssertNumberOfCalls(t, "Errorf", 1)
	})

	t.Run("should return done when all mocks were called", func(t *testing.T) {
		ft := testmock.NewFakeT()

		m1.Inc()

		require.False(t, mug.Called())
		mug.AssertCalled(ft)

		m2.Inc()
		m3.Inc()

		ft.AssertNumberOfCalls(t, "Errorf", 1)
		require.True(t, mug.AssertCalled(t))
		require.True(t, mug.Called())
		require.True(t, sip.Times(t, mug, 3))
		require.False(t, sip.Times(ft, mug, 1))
		require.Equal(t, 0, len(mug.ListPending()))
		require.False(t, mug.Pending())
	})

	t.Run("should return total hits from store", func(t *testing.T) {
		require.EqualValues(t, 3, mug.Hits())
		sip.Times(t, mug, 3)
	})

	t.Run("should clean all store associated with NewScope when calling .Clean()", func(t *testing.T) {
		err := mug.Empty(context.Background())
		require.NoError(t, err)

		require.Equal(t, 0, len(mug.ListPending()))
		require.True(t, sip.Times(t, mug, 0))
		require.False(t, mug.Pending())
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

	mug := vlatte.Pour(nil, []*xhttp.HTTPMock{m1, m2, m3})

	require.True(t, mug.Toss(m1.ID()))
	require.False(t, mug.Toss("unknown"))

	require.Nil(t, mug.Bean(m1.ID()))
}
