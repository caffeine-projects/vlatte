package admin_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().Admin(admin.MustNew())
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	doc, err := os.ReadFile("testdata/express.mock.json")
	require.NoError(t, err)
	sendAdminRequest(t, http.MethodPost, m.AdminURL()+"/mocks", doc, http.StatusCreated)

	client := pb.NewTrackingClient(m.MustDial())
	request := &pb.ParcelRef{Id: "express"}
	mockURL := m.AdminURL() + "/mocks/express"

	reply, err := client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "priority", reply.GetState())

	sendAdminRequest(t, http.MethodPatch, mockURL, []byte(`{"enabled":false}`), http.StatusOK)
	_, err = client.GetParcel(t.Context(), request)
	require.Equal(t, codes.NotFound, status.Code(err))

	sendAdminRequest(t, http.MethodPatch, mockURL, []byte(`{"enabled":true}`), http.StatusOK)
	reply, err = client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "priority", reply.GetState())
}

func TestProgrammaticMocks(t *testing.T) {
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().Admin(admin.MustNew())
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		ID("express").
		Field("id", match.Equal("express")).
		Reply(mockgrpc.Message(&pb.ParcelStatus{State: "priority"})))

	client := pb.NewTrackingClient(m.MustDial())
	request := &pb.ParcelRef{Id: "express"}
	mockURL := m.AdminURL() + "/mocks/express"

	reply, err := client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "priority", reply.GetState())

	sendAdminRequest(t, http.MethodPatch, mockURL, []byte(`{"enabled":false}`), http.StatusOK)
	_, err = client.GetParcel(t.Context(), request)
	require.Equal(t, codes.NotFound, status.Code(err))

	sendAdminRequest(t, http.MethodPatch, mockURL, []byte(`{"enabled":true}`), http.StatusOK)
	reply, err = client.GetParcel(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "priority", reply.GetState())
}

func sendAdminRequest(t *testing.T, method, url string, body []byte, wantStatus int) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()

	got, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, wantStatus, response.StatusCode, string(got))
}
