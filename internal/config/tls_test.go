package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadTLS_ProblemsNameTheSettingsOfItsPrefix(t *testing.T) {
	// HTTP and gRPC servers name their TLS files under keys of their own: a problem names the key to set.
	cert := Value{Key: "grpc.tls.cert", Origin: "flag --grpc-tls-cert", Str: "cert.pem"}

	loaded, problems := LoadTLS(map[string]Value{"grpc.tls.cert": cert}, "grpc.tls.")
	require.Equal(t, TLS{}, loaded)
	require.Equal(t, []Problem{{
		Code: CodeMissingValue, Origin: "flag --grpc-tls-cert", Key: "grpc.tls.key",
		Message: "grpc.tls.cert is set but grpc.tls.key is not", Hint: "set both",
	}}, problems)
}

func TestCertPool_AFileWithoutCertificatesFails(t *testing.T) {
	// An empty pool would reject every client certificate, with a handshake error that names no file.
	file := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(file, []byte("no PEM here"), 0o600))

	_, err := CertPool(file)
	require.EqualError(t, err, file+" has no PEM certificates")
}
