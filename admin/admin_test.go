package admin_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestNew_RefusesAMalformedAddress(t *testing.T) {
	// The address is checked as the admin API is made, in the user's code, not when the mock server starts.
	_, err := admin.New(admin.Addr("localhost"))
	require.EqualError(t, err, `admin: address "localhost": expected host:port, :port or a port number`)

	require.PanicsWithError(t, `admin: address "localhost": expected host:port, :port or a port number`, func() {
		admin.MustNew(admin.Addr("localhost"))
	})

	for _, addr := range []string{"", "8081", ":8081", "127.0.0.1:8081"} {
		_, err := admin.New(admin.Addr(addr))
		require.NoError(t, err, addr)
	}
}

func TestNew_RefusesAMalformedDocsVersion(t *testing.T) {
	// The version goes into the documentation page: only a version of an npm package, checked in the user's code.
	for _, version := range []string{"latest", "1.80", "v1.80.0", `1.80.0"><script>`} {
		_, err := admin.New(admin.DocsVersion(version))
		require.EqualError(t, err, fmt.Sprintf("admin: docs version %q: expected a version such as 1.80.0", version))
	}

	for _, version := range []string{"", "1.80.0", "2.0.0-beta.1"} {
		_, err := admin.New(admin.DocsVersion(version))
		require.NoError(t, err, version)
	}
}

func TestAdmin_DocsLoadTheVersionItIsGiven(t *testing.T) {
	// A user takes a newer Scalar before vlatte pins it: the page at the root loads it, unchecked by a hash it lacks.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew(admin.DocsVersion("1.80.0"))))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	res, err := http.Get(m.AdminURL() + "/")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, string(body), `src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.80.0/dist/browser/standalone.js"`)
	require.NotContains(t, string(body), "integrity=")
}

func TestAdmin_ServesTheMockServerItIsGiven(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew(admin.Addr("127.0.0.1:0"))))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	res, err := http.Get(m.AdminURL() + "/health")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, string(body), `"kind": "http"`)
}

func TestAdmin_ServesOneMockServer(t *testing.T) {
	// An admin API listens on one address, for the mocks of one server: a second server would share neither.
	api := admin.MustNew()

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(api))
	t.Cleanup(m.Close)

	_, err := mockhttp.New(mockhttp.Setup().Silent().Admin(api))
	require.ErrorIs(t, err, admin.ErrServing)

	_, err = mockgrpc.New(mockgrpc.Setup().Admin(api))
	require.ErrorIs(t, err, admin.ErrServing)
}

func TestAdmin_MiddlewaresGuardTheAdminAPI(t *testing.T) {
	// The admin API changes the mocks of a running server: a middleware can ask its clients for a token, and answer
	// those without one in its place, so no operation runs for them.
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	api := admin.MustNew(admin.Addr("127.0.0.1:0"), admin.Use(authenticate))
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(api))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Get(match.URLPath("/hello")).Reply(mockhttp.OK()))

	r, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, m.AdminURL()+"/mocks", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	requireStatus(t, m.URL("/hello"), http.StatusOK, "a request without the token removes no mock")

	r.Header.Set("Authorization", "Bearer secret")

	res, err = http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	requireStatus(t, m.URL("/hello"), mockhttp.StatusNoMatch, "a request with the token removes the mocks")
}

func requireStatus(t *testing.T, url string, status int, msg string) {
	t.Helper()

	res, err := http.Get(url)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, status, res.StatusCode, msg)
}

func TestAdmin_ServeMCPNeedsTheOptionAndAMockServer(t *testing.T) {
	err := admin.MustNew().ServeMCP(t.Context(), strings.NewReader(""), io.Discard)
	require.EqualError(t, err, "admin: MCP is off: create the admin API with the MCP option")

	err = admin.MustNew(admin.MCP()).ServeMCP(t.Context(), strings.NewReader(""), io.Discard)
	require.EqualError(t, err, "admin: the admin API serves no mock server yet: create one with it first")
}

func TestAdmin_MCPIsOffUnlessAsked(t *testing.T) {
	// MCP is a door to every operation: POST /mcp answers that the server can't serve it, unless the option opens it.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew(admin.Addr("127.0.0.1:0"))))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	res, err := http.Post(m.AdminURL()+"/mcp", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNotImplemented, res.StatusCode)
}

func TestAdmin_AServerItCantServeLeavesItFree(t *testing.T) {
	// A mock server the admin API refuses, such as one without its mocks, doesn't take it: the next one gets it.
	api := admin.MustNew()

	_, err := api.Serve(adminapi.Capabilities{})
	require.EqualError(t, err, "admin: no mocks: the capabilities need Mocks")

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(api))
	t.Cleanup(m.Close)
}
