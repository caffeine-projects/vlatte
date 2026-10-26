package mockgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

const (
	_tlsCertFile       = "testdata/cert/cert.pem"
	_tlsKeyFile        = "testdata/cert/key.pem"
	_tlsClientCertFile = "testdata/cert/cert_client.pem"
	_tlsClientKeyFile  = "testdata/cert/key_client.pem"
)

// startTLS starts a server of opts with TLS, with a mock that answers Greetings with "hi".
func startTLS(t *testing.T, opts *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(opts.Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "hi"})))

	return m
}

// dialTLS dials m with TLS conf, which Close closes.
func dialTLS(t *testing.T, m *Server, conf *tls.Config) pb.TestServiceClient {
	t.Helper()

	return pb.NewTestServiceClient(m.MustDial(grpc.WithTransportCredentials(credentials.NewTLS(conf))))
}

// certPool returns a pool of the certificates in file.
func certPool(t *testing.T, file string) *x509.CertPool {
	t.Helper()

	pem, err := os.ReadFile(file)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(pem))

	return pool
}

func TestStartTLS_ServesASelfSignedCertificateDialTrusts(t *testing.T) {
	// gRPC mocks served TLS only through ServerOptions, with certificates a test had to make. StartTLS serves a
	// certificate of its own, as httptest does, and Dial trusts it.
	m := startTLS(t, Setup())

	got, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)

	cert := m.Certificate()
	require.NotNil(t, cert)
	require.Equal(t, []string{"localhost"}, cert.DNSNames)

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	got, code = greetWith(t, dialTLS(t, m, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}), "hello")
	require.Equal(t, codes.OK, code, "a client that trusts Certificate is served")
	require.Equal(t, "hi", got)

	plain := pb.NewTestServiceClient(m.MustDial(grpc.WithTransportCredentials(insecure.NewCredentials())))
	_, code = greetWith(t, plain, "hello")
	require.Equal(t, codes.Unavailable, code, "a client without TLS is refused")
}

func TestStartTLS_ServesInMemory(t *testing.T) {
	// An in-memory server has no host name, which the certificate names: Dial trusts the certificate whatever it dials.
	m := startTLS(t, Setup().InMemory())

	got, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)
}

func TestStartTLS_ServesTheCertificateOfItsFiles(t *testing.T) {
	// A client that trusts the certificate a test gives the server, and not the server itself, is served.
	m := startTLS(t, Setup().TLSCertKeyPair(_tlsCertFile, _tlsKeyFile))

	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)
	require.Equal(t, cert.Leaf.Raw, m.Certificate().Raw)

	conf := &tls.Config{RootCAs: certPool(t, _tlsCertFile), ServerName: "localhost", MinVersion: tls.VersionTLS12}
	got, code := greetWith(t, dialTLS(t, m, conf), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)
}

func TestStartTLS_MutualTLSRequiresAClientCertificate(t *testing.T) {
	// With client CAs, a client without a certificate they signed is refused, as an mTLS service refuses it.
	m := startTLS(t, Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))

	_, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.Unavailable, code, "Dial presents no client certificate")

	clientCert, err := tls.LoadX509KeyPair(_tlsClientCertFile, _tlsClientKeyFile)
	require.NoError(t, err)

	conf := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool(t, _tlsCertFile),
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS12,
	}
	got, code := greetWith(t, dialTLS(t, m, conf), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)
}

func TestStartTLS_ServesTheCertificatesAndClientCAsOfCode(t *testing.T) {
	// Certificates a test made in memory, without a parsed leaf, serve as files do: Certificate parses the leaf.
	loaded, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	cert := tls.Certificate{Certificate: loaded.Certificate, PrivateKey: loaded.PrivateKey}
	m := startTLS(t, Setup().TLSCertificates([]tls.Certificate{cert}).TLSClientCAs(certPool(t, _tlsClientCertFile)))
	require.Equal(t, loaded.Leaf.Raw, m.Certificate().Raw)

	_, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.Unavailable, code, "the client CAs require a client certificate")
}

func TestStartTLS_TLSConfigWinsOverTheCertificates(t *testing.T) {
	// A TLS config of the test's own, such as one that picks its certificate per client, serves as it is.
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	m := startTLS(t, Setup().
		TLSCertificates([]tls.Certificate{mustSelfSigned(t)}).
		TLSConfig(&tls.Config{
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil },
			MinVersion:     tls.VersionTLS12,
		}))
	require.Nil(t, m.Certificate(), "the server can't tell the certificate a client gets")

	_, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.Unavailable, code, "Dial trusts the system's CAs, which didn't sign it")

	conf := &tls.Config{RootCAs: certPool(t, _tlsCertFile), ServerName: "localhost", MinVersion: tls.VersionTLS12}
	got, code := greetWith(t, dialTLS(t, m, conf), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)
}

func TestStartTLS_StartsOnce(t *testing.T) {
	// A server starts once, with TLS or without: Certificate tells a server that serves TLS.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	require.Nil(t, m.Certificate(), "Start serves no TLS")
	require.ErrorIs(t, m.StartTLS(), vlatte.ErrStarted)
	require.PanicsWithError(t, "server: StartTLS: "+vlatte.ErrStarted.Error(), m.MustStartTLS)

	m.Close()
	require.ErrorIs(t, m.StartTLS(), grpc.ErrServerStopped)
}

func TestSetup_TLSFilesItCantLoadFailNew(t *testing.T) {
	// A typo in a file name failed at the first call, as a handshake error: New names the file.
	_, err := New(Setup().ConfigSources().TLSCertKeyPair("testdata/cert/nope.pem", _tlsKeyFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "config: loading the TLS certificate and key")

	_, err = New(Setup().ConfigSources().TLSMutual(_tlsCertFile, _tlsKeyFile, "testdata/cert/nope.pem"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "config: loading the TLS client CAs")
}

func TestStartTLS_CredentialsOfServerOptionsWin(t *testing.T) {
	// A test that set TLS through ServerOptions, as it had to before StartTLS, keeps its credentials.
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	m := startTLS(t, Setup().ServerOptions(grpc.Creds(creds)))

	conf := &tls.Config{RootCAs: certPool(t, _tlsCertFile), ServerName: "localhost", MinVersion: tls.VersionTLS12}
	got, code := greetWith(t, dialTLS(t, m, conf), "hello")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)
}

// mustSelfSigned returns a self-signed certificate.
func mustSelfSigned(t *testing.T) tls.Certificate {
	t.Helper()

	cert, err := selfSigned()
	require.NoError(t, err)

	return cert
}

func TestStartTLS_ACertificateItCantReadFailsTheStart(t *testing.T) {
	// A start that fails closes the server, and its listener, as Start does: nothing listens without a server behind
	// it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Silent().Listener(l).TLSCertificates([]tls.Certificate{{Certificate: [][]byte{[]byte("not DER")}}}))
	t.Cleanup(m.Close)

	err = m.StartTLS()
	require.ErrorContains(t, err, "server: StartTLS: reading the TLS certificate: x509: malformed certificate")
	require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)

	_, err = l.Accept()
	require.ErrorIs(t, err, net.ErrClosed)
}
