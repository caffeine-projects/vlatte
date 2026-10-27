package mockgrpc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// _selfSignedValidity is how long the self-signed certificate of StartTLS is valid.
const _selfSignedValidity = 24 * time.Hour

// serverCreds are the transport credentials of the server, which grpc takes when it is built: none until StartTLS
// sets TLS ones, before the server serves. conns holds the connections they handshook, by their key: see cutConn.
type serverCreds struct {
	credentials.TransportCredentials

	conns sync.Map
}

// StartTLS starts the mock server as Start does, serving TLS: Config.TLSConfig, or the certificates of
// Config.TLSCertificates, requiring a client certificate when Config.TLSClientCAs is set. Without either, it serves
// a self-signed certificate for localhost, 127.0.0.1 and ::1, valid for a day: see Certificate. Dial trusts the
// certificate the server serves. Credentials that ServerOptions set with grpc.Creds win over all of these: the server
// serves them as Start does, Certificate returns nil, and Dial dials as it does after Start, so pass it
// grpc.WithTransportCredentials.
func (app *Server) StartTLS() error {
	return app.start("StartTLS", true)
}

// MustStartTLS is like StartTLS, but panics if it fails.
func (app *Server) MustStartTLS() {
	if err := app.StartTLS(); err != nil {
		panic(err)
	}
}

// Certificate returns the certificate the server serves since StartTLS, for clients to trust, as
// [crypto/x509.CertPool.AddCert] does. It returns nil before StartTLS, after Start, for a Config.TLSConfig without
// certificates, such as one with GetCertificate, and for a server whose ServerOptions set grpc.Creds.
func (app *Server) Certificate() *x509.Certificate {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.cert
}

// serveTLS sets the server's credentials to TLS, before it serves.
func (app *Server) serveTLS() error {
	// The server serves the credentials of its options: what StartTLS would set, it never serves.
	if !app.ownsCreds {
		return nil
	}

	conf := app.config.TLSConfig
	if conf == nil {
		certs := app.config.TLSCertificates
		if len(certs) == 0 {
			cert, err := selfSigned()
			if err != nil {
				return fmt.Errorf("making a self-signed certificate: %w", err)
			}

			certs = []tls.Certificate{cert}
		}

		conf = &tls.Config{Certificates: certs, MinVersion: tls.VersionTLS12}
		if app.config.TLSClientCAs != nil {
			conf.ClientCAs = app.config.TLSClientCAs
			conf.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	if len(conf.Certificates) > 0 && len(conf.Certificates[0].Certificate) > 0 {
		leaf, err := leafOf(conf.Certificates[0])
		if err != nil {
			return fmt.Errorf("reading the TLS certificate: %w", err)
		}

		app.cert = leaf
	}

	app.creds.TransportCredentials = credentials.NewTLS(conf)
	app.tls = true

	return nil
}

// clientTLS returns the TLS configuration of a client that trusts the certificate the server serves, under the first
// host name it has, or its first IP address, whatever address the client dials, such as that of an in-memory server.
// Without a certificate it trusts the system's CAs.
func (app *Server) clientTLS() *tls.Config {
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if app.cert == nil {
		return conf
	}

	conf.RootCAs = x509.NewCertPool()
	conf.RootCAs.AddCert(app.cert)

	switch {
	case len(app.cert.DNSNames) > 0:
		conf.ServerName = app.cert.DNSNames[0]
	case len(app.cert.IPAddresses) > 0:
		conf.ServerName = app.cert.IPAddresses[0].String()
	}

	return conf
}

// keepsCreds reports whether srv handshakes with creds, the credentials New gave it first: grpc keeps the last
// credentials of its options, and has no API that returns them, so this reads its field. A grpc that renames the field
// counts as keeping creds, as it does unless ServerOptions set grpc.Creds.
func keepsCreds(srv *grpc.Server, creds *serverCreds) bool {
	kept := reflect.ValueOf(srv).Elem().FieldByName("opts").FieldByName("creds")

	return !kept.IsValid() || kept.Elem().Kind() == reflect.Pointer && kept.Elem().Pointer() == reflect.ValueOf(creds).Pointer()
}

// leafOf returns the leaf certificate of cert, parsing it when cert has none parsed.
func leafOf(cert tls.Certificate) (*x509.Certificate, error) {
	if cert.Leaf != nil {
		return cert.Leaf, nil
	}

	return x509.ParseCertificate(cert.Certificate[0])
}

// selfSigned makes a self-signed certificate for the local addresses, valid for _selfSignedValidity.
func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// CreateCertificate makes the serial number of a template without one.
	now := time.Now()
	template := &x509.Certificate{
		Subject:               pkix.Name{CommonName: "vlatte", Organization: []string{"vlatte"}},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(_selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, err
}
