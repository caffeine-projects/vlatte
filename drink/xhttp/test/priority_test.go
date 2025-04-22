package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/caffeine-projects/vlatte/drink/xhttp"
	. "github.com/caffeine-projects/vlatte/filter"
)

func TestPriority(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	one := m.MustBrew(Get(URLPath("/test")).
		Priority(3).
		Reply(OK()))

	two := m.MustBrew(Get(URLPath("/test")).
		Priority(1).
		Reply(BadRequest()))

	three := m.MustBrew(Get(URLPath("/test")).
		Priority(100).
		Reply(Created()))

	for i := 0; i < 5; i++ {
		res, err := http.Get(m.URL() + "/test")

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		assert.False(t, one.Called())
		assert.True(t, two.Called())
		assert.False(t, three.Called())
	}
}

func TestPriority_DefaultIsZero(t *testing.T) {
	httpClient := &http.Client{}
	m := New(Setup().
		MockFilePatterns(
			"testdata/priority/default_is_zero/*.json",
			"testdata/priority/default_is_zero/*.yaml"))
	m.MustStart()

	defer m.Close()

	testCases := []struct {
		status int
		url    string
	}{
		{http.StatusNoContent, "/test"},
		{http.StatusNoContent, "/test/hello"},
		{http.StatusNoContent, "/test/hello/world"},
		{http.StatusNotFound, "/hello"},
		{http.StatusNotFound, "/TEST"},
		{http.StatusNotFound, "/TEST/hello"},
		{http.StatusNotFound, "/TEST/HELLO"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			res, err := httpClient.Get(m.URL() + tc.url)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}

func TestPriority_LowestShouldBeServed(t *testing.T) {
	httpClient := &http.Client{}
	m := New(Setup().
		RootDir("testdata/priority").
		MockFilePatterns(
			"lowest_should_be_served/*.json",
			"lowest_should_be_served/*.yaml"))
	m.MustStart()

	defer m.Close()

	testCases := []struct {
		status int
		url    string
	}{
		{http.StatusNoContent, "/test"},
		{http.StatusNoContent, "/test/hello"},
		{http.StatusNoContent, "/test/hello/world"},
		{http.StatusNotFound, "/hello"},
		{http.StatusNotFound, "/TEST"},
		{http.StatusNotFound, "/TEST/hello"},
		{http.StatusNotFound, "/TEST/HELLO"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			res, err := httpClient.Get(m.URL() + tc.url)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}
