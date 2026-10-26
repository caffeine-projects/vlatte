package admin_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
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
