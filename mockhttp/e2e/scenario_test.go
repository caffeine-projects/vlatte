package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestScenarios(t *testing.T) {
	testCases := []struct {
		name string
		s1   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
		s2   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
		s3   vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
	}{
		{
			"code",
			mockhttp.Get(match.URLPath("/1")).
				StartScenario("code").
				ScenarioStateWillBe("step2").
				Name("step-1").
				Reply(mockhttp.OK().PlainText("step1")),
			mockhttp.Get(match.URLPath("/2")).
				ScenarioIs("code").
				ScenarioStateIs("step2").
				ScenarioStateWillBe("step3").
				Name("step-2").
				Reply(mockhttp.OK().PlainText("step2")),
			mockhttp.Get(match.URLPath("/3")).
				ScenarioIs("code").
				ScenarioStateIs("step3").
				ScenarioStateWillBe("step4").
				Name("step-3").
				Reply(mockhttp.OK().PlainText("step3")),
		},

		{
			"file",
			mockhttp.FromFile("testdata/scenario/step_1.yaml"),
			mockhttp.FromFile("testdata/scenario/step_2.yaml"),
			mockhttp.FromFile("testdata/scenario/step_3.yaml"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := mockhttp.MustNew()
			t.Cleanup(func() { m.Close() })
			m.MustStart()

			s1 := m.MustMock(tc.s1)
			s2 := m.MustMock(tc.s2)
			s3 := m.MustMock(tc.s3)

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
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

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
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

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
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
		})
	}
}

func TestScenarios_EachKeyPlaysInAnInstanceOfItsOwn(t *testing.T) {
	// Tenants of one server, or tests that run at once, each move their own checkout: the key renders with the
	// request, its path parameters included, and a reply reads the state of its own key.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.FromBytes([]byte(`{
			"scenario": {"name": "checkout", "required_state": "started", "new_state": "paid", "key": "{{ .Request.PathParams.tenant }}"},
			"request": {"method": "POST", "path_template": "/tenants/{tenant}/pay"},
			"response": {"status": 200, "body": "{{ .Scenarios.StateOf \"checkout\" .Request.PathParams.tenant }}", "template": {"enabled": true}}
		}`), "json"),
		mockhttp.Getf("/order").
			Reply(mockhttp.OK().BodyTemplate(`{{ .Scenarios.StateOf "checkout" (.Request.URL.Query.Get "tenant") }}`)),
	)

	res, body := postBody(t, m.URL("/tenants/a/pay"), "", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "paid", string(body), "the reply reads the state its own request moved")

	res, _ = postBody(t, m.URL("/tenants/a/pay"), "", nil)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, "a paid once")

	res, _ = postBody(t, m.URL("/tenants/b/pay"), "", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "b pays in its own instance")

	_, order := getTarget(t, m, "/order?tenant=c")
	require.Equal(t, matchscenario.StateStarted, string(order))

	_, order = getTarget(t, m, "/order?tenant=a")
	require.Equal(t, "paid", string(order))
}
