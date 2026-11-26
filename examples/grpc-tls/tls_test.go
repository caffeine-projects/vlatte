package tls_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

const (
	_serverCert = "../../mockgrpc/testdata/cert/cert.pem"
	_serverKey  = "../../mockgrpc/testdata/cert/key.pem"
	_clientCert = "../../mockgrpc/testdata/cert/cert_client.pem"
	_clientKey  = "../../mockgrpc/testdata/cert/key_client.pem"
)

func TestFileMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory().
		MockFiles("testdata/lookup.mock.yaml"))
	t.Cleanup(server.Close)
	server.MustStartTLS()
	checkTLSLookup(t, server)

	mutual := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory().
		MockFiles("testdata/lookup.mock.yaml").
		TLSMutual(_serverCert, _serverKey, _clientCert))
	t.Cleanup(mutual.Close)
	mutual.MustStartTLS()
	checkMutualTLSLookup(t, mutual)
}

func TestProgrammaticMocks(t *testing.T) {
	server := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory())
	t.Cleanup(server.Close)
	server.MustStartTLS()
	server.MustMock(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Message(&pb.ParcelRef{Id: "P1"}).
		Reply(mockgrpc.Message(&pb.ParcelStatus{State: "in_transit"})))
	checkTLSLookup(t, server)

	mutual := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().Silent().InMemory().
		TLSMutual(_serverCert, _serverKey, _clientCert))
	t.Cleanup(mutual.Close)
	mutual.MustStartTLS()
	mutual.MustMock(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Message(&pb.ParcelRef{Id: "P1"}).
		Reply(mockgrpc.Message(&pb.ParcelStatus{State: "in_transit"})))
	checkMutualTLSLookup(t, mutual)
}

func checkTLSLookup(t *testing.T, server *mockgrpc.Server) {
	t.Helper()

	// After MustStartTLS, MustDial trusts the server's certificate.
	trusted := pb.NewTrackingClient(server.MustDial())
	plain := pb.NewTrackingClient(server.MustDial(grpc.WithTransportCredentials(insecure.NewCredentials())))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ref := &pb.ParcelRef{Id: "P1"}

	res, err := trusted.GetParcel(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "in_transit", res.GetState())

	_, err = plain.GetParcel(ctx, ref)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func checkMutualTLSLookup(t *testing.T, server *mockgrpc.Server) {
	t.Helper()

	serverPEM, err := os.ReadFile(_serverCert)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(serverPEM))
	identity, err := tls.LoadX509KeyPair(_clientCert, _clientKey)
	require.NoError(t, err)

	trust := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	withIdentity := trust.Clone()
	withIdentity.Certificates = []tls.Certificate{identity}

	identified := pb.NewTrackingClient(server.MustDial(
		grpc.WithTransportCredentials(credentials.NewTLS(withIdentity)),
	))
	unidentified := pb.NewTrackingClient(server.MustDial(
		grpc.WithTransportCredentials(credentials.NewTLS(trust)),
	))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ref := &pb.ParcelRef{Id: "P1"}

	res, err := identified.GetParcel(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "in_transit", res.GetState())

	_, err = unidentified.GetParcel(ctx, ref)
	require.Equal(t, codes.Unavailable, status.Code(err))
}
