package kcore_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/khttp"
	"github.com/caffeine-projects/vlatte/khttp/test/testmock"
	"github.com/caffeine-projects/vlatte/matcher"
	"github.com/stretchr/testify/require"
)

func TestScoped(t *testing.T) {
	type testMock struct {
		*kcore.BaseMock
	}

	newTestMock := func() *testMock {
		return &testMock{BaseMock: kcore.NewMock()}
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	repo := kcore.NewStore[*testMock]()
	repo.Save(context.Background(), m1, m2, m3)

	scoped := kcore.Scoped(repo, []*testMock{m1, m2, m3})

	require.Equal(t, 3, len(scoped.GetAll()))
	require.Equal(t, m1, scoped.Get(m1.GetID()))
	require.Nil(t, scoped.Get("unknown"))

	t.Run("should not return done when there is still pending mocks", func(t *testing.T) {
		ft := testmock.NewFakeT()

		require.False(t, scoped.Called())
		require.Equal(t, 3, len(scoped.GetPending()))
		require.True(t, scoped.Pending())

		scoped.AssertCalled(ft)
		ft.AssertNumberOfCalls(t, "Errorf", 1)
	})

	t.Run("should return done when all mocks were called", func(t *testing.T) {
		ft := testmock.NewFakeT()

		m1.Inc()

		require.False(t, scoped.Called())
		scoped.AssertCalled(ft)

		m2.Inc()
		m3.Inc()

		ft.AssertNumberOfCalls(t, "Errorf", 1)
		require.True(t, scoped.AssertCalled(t))
		require.True(t, scoped.Called())
		require.True(t, scoped.AssertNumberOfCalls(t, 3))
		require.False(t, scoped.AssertNumberOfCalls(ft, 1))
		require.Equal(t, 0, len(scoped.GetPending()))
		require.False(t, scoped.Pending())
	})

	t.Run("should return total hits from store", func(t *testing.T) {
		require.EqualValues(t, 3, scoped.Hits())
		scoped.AssertNumberOfCalls(t, 3)
	})

	t.Run("should clean all store associated with NewScope when calling .Clean()", func(t *testing.T) {
		err := scoped.Clean(context.Background())
		require.NoError(t, err)

		require.Equal(t, 0, len(scoped.GetPending()))
		require.True(t, scoped.AssertNumberOfCalls(t, 0))
		require.False(t, scoped.Pending())
	})

	t.Run("should only consider enabled store", func(t *testing.T) {
		m := khttp.NewAPI()
		m.MustStart()

		defer m.Close()

		s1 := m.MustMock(khttp.Get(matcher.URLPath("/test1")).Reply(khttp.OK()))
		s2 := m.MustMock(
			khttp.Get(matcher.URLPath("/test2")).Reply(khttp.OK()),
			khttp.Get(matcher.URLPath("/test3")).Reply(khttp.OK()))

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
			require.Equal(t, khttp.StatusNoMatch, res.StatusCode)

			res, err = http.Get(fmt.Sprintf("%s/test2", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)

			res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, int64(1), s1.Hits())

			s1.AssertNumberOfCalls(t, 1)
		})

		t.Run("enabling previously disabled", func(t *testing.T) {
			s1.Enable()

			res, err := http.Get(fmt.Sprintf("%s/test1", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.EqualValues(t, 2, s1.Hits())

			s1.AssertNumberOfCalls(t, 2)
		})

		t.Run("disabling multiple", func(t *testing.T) {
			s2.Disable()

			res, err := http.Get(fmt.Sprintf("%s/test2", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, khttp.StatusNoMatch, res.StatusCode)

			res, err = http.Get(fmt.Sprintf("%s/test3", m.URL()))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, khttp.StatusNoMatch, res.StatusCode)
		})
	})
}

func TestScopedDelete(t *testing.T) {
	type testMock struct {
		*kcore.BaseMock
	}

	newTestMock := func() *testMock {
		return &testMock{BaseMock: kcore.NewMock()}
	}

	m1 := newTestMock()
	m2 := newTestMock()
	m3 := newTestMock()

	scoped := kcore.Scoped(nil, []*testMock{m1, m2, m3})

	require.True(t, scoped.Delete(m1.GetID()))
	require.False(t, scoped.Delete("unknown"))

	require.Nil(t, scoped.Get(m1.GetID()))
}
