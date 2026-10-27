package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestTemplateEnv_MockFilesReadTheVariablesTheConfigAllows(t *testing.T) {
	t.Setenv("VLATTE_E2E_TENANT", "acme")
	t.Setenv("VLATTE_E2E_SECRET", "s3cr3t")

	m := mockhttp.MustNew(mockhttp.Setup().Silent().ConfigSources(mockhttp.ConfigArgs([]string{"--template-env", "VLATTE_E2E_TENANT"})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/templating/template_env.yaml"))

	res, err := http.Get(m.URL() + "/tenant")
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"tenant": "acme"}`, string(b))

	// A variable the config doesn't name fails the mock as it loads, before any request could read it.
	_, err = m.Mock(mockhttp.FromFile("testdata/templating/template_env_denied.yaml"))
	require.ErrorContains(t, err, `env: "VLATTE_E2E_SECRET" is not allowed`)
	require.NotContains(t, err.Error(), "s3cr3t")
}
