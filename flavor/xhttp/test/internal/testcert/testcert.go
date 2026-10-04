// Command testcert writes a self-signed certificate and its key, cert.pem and key.pem, to the working directory.
//
// The certificates in flavor/xhttp/test/testdata/cert and plugin/webhook/testdata/cert come from it:
// cert.pem and key.pem from one run, cert_client.pem and key_client.pem from another,
// so the client identity in mutual TLS tests differs from the server's.
//
//	go run ./flavor/xhttp/test/internal/testcert
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("failed to generate the private key: %v", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("failed to generate the serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{Organization: []string{"httpm"}},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(358000 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &pk.PublicKey, pk)
	if err != nil {
		log.Fatalf("failed to create the certificate: %v", err)
	}

	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if pemCert == nil {
		log.Fatalf("failed to encode certificate to PEM")
	}

	if err = os.WriteFile("cert.pem", pemCert, 0o644); err != nil {
		log.Fatalf("error writing the cert to file: %v", err)
	}

	pkb, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		log.Fatalf("Unable to marshal private key: %v", err)
	}

	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkb})
	if pemKey == nil {
		log.Fatalf("failed to encode key to PEM")
	}

	if err = os.WriteFile("key.pem", pemKey, 0o600); err != nil {
		log.Fatalf("error writing the key: %v", err)
	}
}
