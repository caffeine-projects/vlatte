package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// TLS is the TLS a server's settings name the files of.
type TLS struct {
	// Certificates holds the server's certificate. Empty when the settings name none.
	Certificates []tls.Certificate

	// ClientCAs are the CAs that client certificates must be signed by, for mutual TLS. Nil when the settings name none.
	ClientCAs *x509.CertPool
}

// LoadTLS loads the files that the settings <prefix>cert, <prefix>key and <prefix>ca of values name: the server's
// certificate and its key, which go together, and the client CAs, which need them. It reports a missing setting and a
// file it can't load as problems of the setting that named it.
func LoadTLS(values map[string]Value, prefix string) (TLS, []Problem) {
	certKey, keyKey, caKey := prefix+"cert", prefix+"key", prefix+"ca"
	cert, hasCert := values[certKey]
	key, hasKey := values[keyKey]
	ca, hasCA := values[caKey]

	switch {
	case !hasCert && !hasKey && !hasCA:
		return TLS{}, nil
	case hasCert && !hasKey:
		return TLS{}, []Problem{{
			Code: CodeMissingValue, Origin: cert.Origin, Key: keyKey,
			Message: certKey + " is set but " + keyKey + " is not", Hint: "set both",
		}}
	case hasKey && !hasCert:
		return TLS{}, []Problem{{
			Code: CodeMissingValue, Origin: key.Origin, Key: certKey,
			Message: keyKey + " is set but " + certKey + " is not", Hint: "set both",
		}}
	case !hasCert:
		return TLS{}, []Problem{{
			Code: CodeMissingValue, Origin: ca.Origin, Key: certKey,
			Message: caKey + " is set but " + certKey + " and " + keyKey + " are not",
			Hint:    "mutual TLS needs the server's certificate and key too",
		}}
	}

	var (
		loaded   TLS
		problems []Problem
	)

	if c, err := tls.LoadX509KeyPair(cert.Str, key.Str); err != nil {
		problems = append(problems, FileProblem(cert, "can't load the certificate and key", err))
	} else {
		loaded.Certificates = []tls.Certificate{c}
	}

	if hasCA {
		pool, err := CertPool(ca.Str)
		if err != nil {
			return loaded, append(problems, FileProblem(ca, "can't load the client CAs", err))
		}

		loaded.ClientCAs = pool
	}

	return loaded, problems
}

// CertPool reads the PEM certificates in file.
// A file without any is an error: an empty pool would reject every client certificate.
func CertPool(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s has no PEM certificates", file)
	}

	return pool, nil
}

// FileProblem reports a file that can't be loaded, what names, as a problem of the setting v that named it.
func FileProblem(v Value, what string, err error) Problem {
	p := Problem{
		Code: CodeInvalidValue, Origin: v.Origin, Key: v.Key, Value: v.Str,
		Message: what + ": " + err.Error(),
	}

	if wd, err := os.Getwd(); err == nil && !filepath.IsAbs(v.Str) {
		p.Hint = "relative paths start at " + wd
	}

	return p
}
