package mockgrpc

import (
	"crypto/tls"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestConfigSources_ReadTheGRPCSectionOfTheSharedSources(t *testing.T) {
	// The vlatte command reads one config file, one environment and one command line for its HTTP and gRPC servers:
	// the gRPC server reads its section, and leaves the HTTP settings to the HTTP server.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: http
addr: ":8080"
grpc:
  name: grpc
  addr: ":0"
  mock_files: [a.yaml]
  reflection: true
  no_match_code: UNIMPLEMENTED
  debug_headers: false
  shutdown_timeout: 2s
  log: {level: warn}
  admin: {addr: ":0"}
  journal: {max_entries: 3}
`), 0o600))
	t.Setenv("VLATTE_ADDR", ":9090")
	t.Setenv("VLATTE_GRPC_ROOT_DIR", dir)

	set := writeDescriptorSet(t, "dyn")

	m, err := New(Setup().ConfigSources(ConfigFile(path), ConfigEnv(), ConfigArgs([]string{"--grpc-descriptors=" + set})))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	c := m.Config()
	require.True(t, c.Serve, "a key of the section turns it on")
	require.Equal(t, "grpc", c.Name)
	require.Equal(t, ":0", c.Addr)
	require.Equal(t, dir, c.RootDir)
	require.Equal(t, []string{"a.yaml"}, c.MockFiles)
	require.Equal(t, []string{set}, c.Descriptors)
	require.True(t, c.Reflection)
	require.Equal(t, codes.Unimplemented, c.NoMatchCode)
	require.True(t, c.NoDebugHeaders)
	require.Equal(t, 2*time.Second, c.ShutdownTimeout)
	require.Equal(t, vlatte.LogLevelWarn, c.LogLevel)
	require.Equal(t, &AdminConfig{Addr: ":0"}, c.Admin)
	require.Equal(t, 3, c.Journal.MaxEntries)
}

func TestConfigSources_AnUnknownGRPCSettingFails(t *testing.T) {
	// A typo would otherwise be ignored silently: a VLATTE_GRPC_* variable or a grpc key that is no setting fails.
	t.Setenv("VLATTE_GRPC_ADRR", ":1")

	_, err := New(Setup().ConfigSources(ConfigEnv()))
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "VLATTE_GRPC_ADRR is not a known setting", cfgErr.Problems[0].Message)
	require.Equal(t, "did you mean VLATTE_GRPC_ADDR?", cfgErr.Problems[0].Hint)

	_, err = New(Setup().ConfigSources(ConfigArgs([]string{"--grpc-adrr=:1"})))
	require.ErrorContains(t, err, "grpc-adrr")
}

func TestConfigSources_ByDefaultReadTheConfigFileAndTheEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte("grpc: {name: from-file}\n"), 0o600))

	m, err := New()
	require.NoError(t, err)
	t.Cleanup(m.Close)
	require.Equal(t, "from-file", m.Config().Name)

	none, err := New(Setup().ConfigSources())
	require.NoError(t, err)
	t.Cleanup(none.Close)
	require.Empty(t, none.Config().Name, "no sources read none")
}

func TestConfigSources_HelpAsksForHelp(t *testing.T) {
	_, err := New(Setup().ConfigSources(ConfigArgs([]string{"--help"})))
	require.ErrorIs(t, err, flag.ErrHelp)
}

func TestConfigFlags_AreTheGRPCSection(t *testing.T) {
	// The vlatte command splits its arguments by these flags: each names a gRPC setting, or the config file.
	for _, f := range ConfigFlags() {
		require.True(t, f.Name == "config" || strings.HasPrefix(f.Name, "grpc"), f.Name)
	}

	require.Contains(t, ConfigUsage(), "env VLATTE_GRPC_ADDR, file key grpc.addr")
}

func TestConfigSources_ReadTheRestOfTheGRPCSection(t *testing.T) {
	// A code is a name or a number on a command line too, and the journal keys each set the journal.
	dir := t.TempDir()

	m, err := New(Setup().ConfigSources(ConfigArgs([]string{
		"--grpc-no-match-code=14",
		"--grpc-near-misses=false",
		"--grpc-journal-dir=" + dir,
		"--grpc-journal-max-body-size=64",
	})))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	c := m.Config()
	require.Equal(t, codes.Unavailable, c.NoMatchCode)
	require.True(t, c.NoNearMisses)
	require.Equal(t, dir, c.Journal.Dir)
	require.Equal(t, int64(64), c.Journal.MaxBodySize)
}

func TestConfigSources_AShutdownTimeoutOfZeroFails(t *testing.T) {
	_, err := New(Setup().ConfigSources(ConfigArgs([]string{"--grpc-shutdown-timeout=0"})))
	require.ErrorContains(t, err, "grpc.shutdown_timeout")
}

func TestConfigSources_TLSFilesTurnTLSOn(t *testing.T) {
	// The vlatte command served gRPC mocks without TLS only. The grpc.tls keys name its files, and turn it on, as a
	// key of a section does.
	path := filepath.Join(t.TempDir(), "settings.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
grpc:
  tls:
    cert: `+_tlsCertFile+`
    key: `+_tlsKeyFile+`
    ca: `+_tlsClientCertFile+`
`), 0o600))

	m, err := New(Setup().Silent().ConfigSources(ConfigFile(path)))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	c := m.Config()
	require.True(t, c.Serve)
	require.True(t, c.UseTLS)
	require.NotNil(t, c.TLSClientCAs)

	m.MustStartTLS()
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)
	require.Equal(t, cert.Leaf.Raw, m.Certificate().Raw)

	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "hi"})))
	_, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hello")
	require.Equal(t, codes.Unavailable, code, "grpc.tls.ca requires a client certificate")
}

func TestConfigSources_TLSTurnsTLSOnWithoutFiles(t *testing.T) {
	m, err := New(Setup().ConfigSources(ConfigArgs([]string{"--grpc-tls"})))
	require.NoError(t, err)
	t.Cleanup(m.Close)

	require.True(t, m.Config().UseTLS)
	require.Empty(t, m.Config().TLSCertificates, "StartTLS serves a self-signed certificate")
}

func TestConfigSources_TLSFilesNeedEachOther(t *testing.T) {
	// A certificate without its key, or client CAs without the server's certificate, can't serve TLS: each is a
	// problem of the settings, with the other problems found.
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "cert without key",
			args: []string{"--grpc-tls-cert=" + _tlsCertFile},
			want: "grpc.tls.cert is set but grpc.tls.key is not",
		},
		{
			name: "key without cert",
			args: []string{"--grpc-tls-key=" + _tlsKeyFile},
			want: "grpc.tls.key is set but grpc.tls.cert is not",
		},
		{
			name: "ca without cert and key",
			args: []string{"--grpc-tls-ca=" + _tlsClientCertFile},
			want: "grpc.tls.ca is set but grpc.tls.cert and grpc.tls.key are not",
		},
		{
			name: "a cert that can't load",
			args: []string{"--grpc-tls-cert=" + _tlsKeyFile, "--grpc-tls-key=" + _tlsKeyFile},
			want: "can't load the certificate and key: tls: failed to find certificate PEM data in certificate input, " +
				"but did find a private key; PEM inputs may have been switched",
		},
		{
			name: "client CAs that can't load",
			args: []string{"--grpc-tls-cert=" + _tlsCertFile, "--grpc-tls-key=" + _tlsKeyFile, "--grpc-tls-ca=" + _tlsKeyFile},
			want: "can't load the client CAs: " + _tlsKeyFile + " has no PEM certificates",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Setup().ConfigSources(ConfigArgs(append(tc.args, "--grpc-shutdown-timeout=0"))))
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Len(t, cfgErr.Problems, 2)
			require.Equal(t, tc.want, cfgErr.Problems[1].Message)
		})
	}
}
