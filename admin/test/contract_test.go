package admin_test

import (
	"io"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestAdmin_ConfigurationRejectsCustomImplementations(t *testing.T) {
	// Both configuration paths take the built-in handle, not a caller's replacement admin server.
	for _, target := range []reflect.Type{
		reflect.TypeFor[mockhttp.AdminAPI](),
		reflect.TypeFor[mockgrpc.AdminAPI](),
	} {
		require.False(t, reflect.TypeFor[customAdmin]().AssignableTo(target), target.String())
		require.True(t, reflect.TypeFor[*admin.Admin]().AssignableTo(target), target.String())
	}
}

func TestAdmin_ExportsNoCapabilityBinding(t *testing.T) {
	_, exposed := reflect.TypeFor[*admin.Admin]().MethodByName("Serve")
	require.False(t, exposed, "capabilities are bound inside the repository")
}

func TestAdmin_DirectConfigurationAcceptsTheBuiltInHandle(t *testing.T) {
	httpServer, err := mockhttp.New(&mockhttp.Config{Admin: admin.MustNew()}, mockhttp.Setup().Silent())
	require.NoError(t, err)
	t.Cleanup(httpServer.Close)

	grpcServer, err := mockgrpc.New(&mockgrpc.Config{Admin: admin.MustNew()})
	require.NoError(t, err)
	t.Cleanup(grpcServer.Close)
}

func TestAdmin_UninitializedHandleNamesTheConstructor(t *testing.T) {
	api := &admin.Admin{}
	want := "admin: uninitialized admin API: use admin.New"

	_, err := mockhttp.New(mockhttp.Setup().Silent().Admin(api))
	require.EqualError(t, err, "server: "+want)

	_, err = mockgrpc.New(mockgrpc.Setup().Admin(api))
	require.EqualError(t, err, "server: "+want)

	require.EqualError(t, api.ServeMCP(t.Context(), nil, io.Discard), want)
}

type customAdmin struct{}

func (customAdmin) Serve(adminapi.Capabilities) (adminapi.Server, error) {
	return nil, nil
}
