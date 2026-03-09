package test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp"
	. "github.com/caffeine-projects/vlatte/filter"
)

func TestScenarios(t *testing.T) {
	testCases := []struct {
		name string
		s1   vlatte.Grinder[*xhttp.HTTPMock, *xhttp.HTTPMockApp]
		s2   vlatte.Grinder[*xhttp.HTTPMock, *xhttp.HTTPMockApp]
		s3   vlatte.Grinder[*xhttp.HTTPMock, *xhttp.HTTPMockApp]
	}{
		{"code",
			xhttp.Get(URLPath("/1")).
				StartScenario("code").
				ScenarioStateWillBe("step2").
				Name("step-1").
				Reply(xhttp.OK().PlainText("step1")),
			xhttp.Get(URLPath("/2")).
				ScenarioIs("code").
				ScenarioStateIs("step2").
				ScenarioStateWillBe("step3").
				Name("step-2").
				Reply(xhttp.OK().PlainText("step2")),
			xhttp.Get(URLPath("/3")).
				ScenarioIs("code").
				ScenarioStateIs("step3").
				ScenarioStateWillBe("step4").
				Name("step-3").
				Reply(xhttp.OK().PlainText("step3"))},

		{"file",
			xhttp.FromFile("testdata/scenario/step_1.yaml"),
			xhttp.FromFile("testdata/scenario/step_2.yaml"),
			xhttp.FromFile("testdata/scenario/step_3.yaml")},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := xhttp.New()
			t.Cleanup(func() { m.Close() })
			m.MustStart()

			s1 := m.MustBrew(tc.s1)
			s2 := m.MustBrew(tc.s2)
			s3 := m.MustBrew(tc.s3)

			// --- step1

			req, _ := http.NewRequest(http.MethodGet, m.URL()+"/1", nil)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s1.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step1", string(body))
			require.NoError(t, res.Body.Close())

			// step1 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/1", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)

			// --- step2

			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/2", nil)
			res, err = http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err = io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s2.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step2", string(body))
			require.NoError(t, res.Body.Close())

			// step2 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/2", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)

			// --- step3

			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/3", nil)
			res, err = http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err = io.ReadAll(res.Body)
			require.NoError(t, err)

			require.True(t, s3.Called())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "step3", string(body))
			require.NoError(t, res.Body.Close())

			// step3 is already in a different state, it should not match anymore.
			req, _ = http.NewRequest(http.MethodGet, m.URL()+"/3", nil)
			res, err = http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)
		})
	}
}
