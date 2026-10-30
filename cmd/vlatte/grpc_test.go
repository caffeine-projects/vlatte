package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSplitArgs_GivesEachServerItsFlags(t *testing.T) {
	// The flags of gRPC settings go to the gRPC server, with their values, the config file to both, and the rest to
	// the HTTP server: each server reports a flag it doesn't know.
	httpArgs, grpcArgs := splitArgs([]string{
		"--addr", ":8080",
		"--grpc",
		"--grpc-addr", ":50051",
		"--grpc-mock-files=mocks/*.yaml",
		"-c", "settings.yaml",
		"--grpc-reflection=false",
		"--grpc-admin-dir", "admin",
		"--debug",
	})

	require.Equal(t, []string{"--addr", ":8080", "-c", "settings.yaml", "--debug"}, httpArgs)
	require.Equal(t, []string{
		"--grpc", "--grpc-addr", ":50051", "--grpc-mock-files=mocks/*.yaml", "--config=settings.yaml",
		"--grpc-reflection=false", "--grpc-admin-dir", "admin",
	}, grpcArgs, "the command's gRPC settings too")
}

func TestSplitArgs_FlagsEndAtTheTerminator(t *testing.T) {
	// The HTTP server's parser stops at -- and rejects what follows: no gRPC flag after it configures the gRPC server.
	httpArgs, grpcArgs := splitArgs([]string{
		"--addr", ":8080", "--grpc-addr", "--", "--", "--grpc-addr=:50051", "--grpc-descriptors=api.binpb", "-c", "x",
	})

	require.Equal(t, []string{
		"--addr", ":8080", "--", "--grpc-addr=:50051", "--grpc-descriptors=api.binpb", "-c", "x",
	}, httpArgs)
	require.Equal(t, []string{"--grpc-addr", "--"}, grpcArgs, "a flag's value can be --")
}

// healthSet writes the descriptors of the health service, renamed into package pkg so that the program has no
// generated code of it, as a descriptor set file, and returns the file and the descriptors.
func healthSet(t *testing.T, pkg string) (string, protoreflect.FileDescriptor) {
	t.Helper()

	fd := protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)
	fd.Name = new(pkg + ".proto")
	fd.Package = new(pkg)

	var rename func(msgs []*descriptorpb.DescriptorProto)
	rename = func(msgs []*descriptorpb.DescriptorProto) {
		for _, msg := range msgs {
			for _, f := range msg.GetField() {
				if f.TypeName != nil {
					f.TypeName = new(strings.Replace(f.GetTypeName(), ".grpc.health.v1.", "."+pkg+".", 1))
				}
			}

			rename(msg.GetNestedType())
		}
	}

	rename(fd.GetMessageType())

	for _, svc := range fd.GetService() {
		for _, m := range svc.GetMethod() {
			m.InputType = new(strings.Replace(m.GetInputType(), ".grpc.health.v1.", "."+pkg+".", 1))
			m.OutputType = new(strings.Replace(m.GetOutputType(), ".grpc.health.v1.", "."+pkg+".", 1))
		}
	}

	file, err := protodesc.NewFile(fd, nil)
	require.NoError(t, err)

	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fd}})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), pkg+".binpb")
	require.NoError(t, os.WriteFile(path, b, 0o600))

	return path, file
}

func TestCLI_ServesGRPCMocksOfDescriptorSets(t *testing.T) {
	// The command served HTTP mocks only. Given descriptor sets and mock files, it serves gRPC mocks of services it
	// has no code of, next to the HTTP ones.
	withoutDockerHost(t)

	set, file := healthSet(t, "mock.health.v1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "check.yaml"), []byte(`
request:
  method: mock.health.v1.Health/Check
  fields: {service: api}
response:
  message: {status: SERVING}
`), 0o600))

	httpAddr, grpcAddr := randomAddr(t), randomAddr(t)
	stop := start(t, "--addr", httpAddr,
		"--grpc-addr", grpcAddr, "--grpc-descriptors", set, "--grpc-mock-files", filepath.Join(dir, "*.yaml"))

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	msgs := file.Messages()
	req := dynamicpb.NewMessage(msgs.ByName("HealthCheckRequest"))
	req.Set(req.Descriptor().Fields().ByName("service"), protoreflect.ValueOfString("api"))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res := dynamicpb.NewMessage(msgs.ByName("HealthCheckResponse"))
		if !assert.NoError(c, conn.Invoke(t.Context(), "/mock.health.v1.Health/Check", req, res)) {
			return
		}

		status := res.Descriptor().Fields().ByName("status")
		assert.Equal(c, status.Enum().Values().ByName("SERVING").Number(), res.Get(status).Enum())
	}, 10*time.Second, 100*time.Millisecond)

	require.Equal(t, 0, stop(), "a stopped command exits cleanly")
}

func TestCLI_GRPCMocksReadTheKVFile(t *testing.T) {
	// kv.file fills the store of both servers: a gRPC mock's template reads it as an HTTP mock's does.
	withoutDockerHost(t)

	set, file := healthSet(t, "kv.health.v1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kv.csv"), []byte("key,value\nstatus,SERVING\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "check.yaml"), []byte(`
request:
  method: kv.health.v1.Health/Check
response:
  message: '{"status": "{{ .KV.Get "status" }}"}'
  template: true
`), 0o600))

	httpAddr, grpcAddr := randomAddr(t), randomAddr(t)
	start(t, "--addr", httpAddr, "--kv-file", filepath.Join(dir, "kv.csv"),
		"--grpc-addr", grpcAddr, "--grpc-descriptors", set, "--grpc-mock-files", filepath.Join(dir, "*.yaml"))

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	msgs := file.Messages()
	req := dynamicpb.NewMessage(msgs.ByName("HealthCheckRequest"))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res := dynamicpb.NewMessage(msgs.ByName("HealthCheckResponse"))
		if !assert.NoError(c, conn.Invoke(t.Context(), "/kv.health.v1.Health/Check", req, res)) {
			return
		}

		status := res.Descriptor().Fields().ByName("status")
		assert.Equal(c, status.Enum().Values().ByName("SERVING").Number(), res.Get(status).Enum())
	}, 10*time.Second, 100*time.Millisecond)
}

func TestRun_InvalidGRPCSettingsLeaveNothingOpen(t *testing.T) {
	// An invalid gRPC setting is a usage error. The HTTP server, built first, kept its journal open: the next run in
	// the directory found it in use.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	var stderr bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"--grpc-no-match-code=NOPE"}, nil, io.Discard, &stderr))
	require.Contains(t, stderr.String(), "grpc.no_match_code")

	stop := start(t, "--addr", randomAddr(t))
	require.Equal(t, 0, stop(), "the next run starts and stops")
}

func TestRun_AGRPCPortInUseFailsTheRun(t *testing.T) {
	// The gRPC server starts after the HTTP one: when it can't listen, the run fails and the HTTP server stops too.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	var stderr bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"--addr", randomAddr(t), "--grpc-addr", l.Addr().String()}, nil, io.Discard, &stderr))
	require.Contains(t, stderr.String(), "address already in use")

	stop := start(t, "--addr", randomAddr(t))
	require.Equal(t, 0, stop(), "the next run starts and stops")
}

func TestCLI_ServesGRPCMocksOverTLS(t *testing.T) {
	// --grpc-tls serves the gRPC mocks over TLS, as --https does the HTTP ones: a client without TLS is refused.
	withoutDockerHost(t)

	grpcAddr := randomAddr(t)
	stop := start(t, "--addr", randomAddr(t), "--grpc-addr", grpcAddr, "--grpc-tls")

	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // its own certificate
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// A method the server has no descriptor of: the server, not the handshake, answers.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		err := conn.Invoke(t.Context(), "/unknown.Service/Method", &emptypb.Empty{}, &emptypb.Empty{})
		assert.Equal(c, codes.Unimplemented, status.Code(err))
	}, 10*time.Second, 100*time.Millisecond)

	plain, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })

	err = plain.Invoke(t.Context(), "/unknown.Service/Method", &emptypb.Empty{}, &emptypb.Empty{})
	require.Equal(t, codes.Unavailable, status.Code(err))

	require.Equal(t, 0, stop(), "a stopped command exits cleanly")
}

func TestRun_ServesTheGRPCAdminAPIItsSettingsSay(t *testing.T) {
	// The gRPC server's admin API is the command's to serve, as the HTTP server's is: --grpc-admin-addr sets where,
	// and --grpc-admin=false turns it off.
	withoutDockerHost(t)

	// Port 0 has the system choose free ports, which no other process can take before the run listens on them.
	_, out := startOutput(t, "--addr", "127.0.0.1:0", "--grpc-addr", "127.0.0.1:0", "--grpc-admin-addr", "127.0.0.1:0",
		"--log-level", "disabled")

	res, err := http.Get(printed(t, out, ", admin API: ") + "/health")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, string(body), `"kind": "grpc"`)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Another run, beside the first, which holds the journal's directory: without a journal.
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(ctx, []string{
		"--addr", "127.0.0.1:0", "--grpc-addr", "127.0.0.1:0", "--grpc-admin=false",
		"--journal=false", "--log-level", "disabled",
	}, nil, &stdout, &stderr), stderr.String())
	require.Contains(t, stdout.String(), ", admin API: \n", "no admin API serves the gRPC server")
}

func TestCLI_KeepsTheGRPCAdminMocksAcrossRestarts(t *testing.T) {
	// The mocks the gRPC server's admin API adds are kept in testdata/vlatte/_grpc_admin, next to the HTTP admin API's,
	// so the next run serves them, as it does the HTTP ones.
	withoutDockerHost(t)

	set, file := healthSet(t, "kept.health.v1")
	t.Chdir(t.TempDir())

	grpcAddr, adminAddr := randomAddr(t), randomAddr(t)
	args := []string{
		"--addr", randomAddr(t), "--grpc-addr", grpcAddr, "--grpc-descriptors", set, "--grpc-admin-addr", adminAddr,
		"--log-level", "disabled",
	}

	stop := start(t, args...)
	postGRPCMock(t, adminAddr, "kept", "kept.health.v1")
	require.FileExists(t, filepath.Join("testdata", "vlatte", "_grpc_admin", "kept.mock.yaml"))
	require.Zero(t, stop())

	start(t, args...)
	requireServing(t, grpcAddr, file, "kept.health.v1")

	t.Run("in memory", func(t *testing.T) {
		// --grpc-admin-dir="" keeps them in memory: nothing is written.
		t.Chdir(t.TempDir())

		adminAddr := randomAddr(t)
		start(t, "--addr", randomAddr(t), "--grpc-addr", randomAddr(t), "--grpc-descriptors", set,
			"--grpc-admin-addr", adminAddr, "--grpc-admin-dir=", "--log-level", "disabled")
		postGRPCMock(t, adminAddr, "gone", "kept.health.v1")

		_, err := os.Stat(filepath.Join("testdata", "vlatte", "_grpc_admin"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("in a named server's work directory", func(t *testing.T) {
		// The gRPC server has no work directory: {workdir} is the HTTP server's, so servers of one directory keep
		// their mocks apart.
		t.Chdir(t.TempDir())

		adminAddr := randomAddr(t)
		start(t, "--addr", randomAddr(t), "--name", "api", "--grpc-addr", randomAddr(t), "--grpc-descriptors", set,
			"--grpc-admin-addr", adminAddr, "--log-level", "disabled")
		postGRPCMock(t, adminAddr, "named", "kept.health.v1")

		require.FileExists(t, filepath.Join("testdata", "vlatte", "api", "_grpc_admin", "named.mock.yaml"))
	})

	t.Run("in the work directory, whatever the gRPC root directory", func(t *testing.T) {
		// grpc.root_dir is where the gRPC mock files start, not the HTTP server's work directory.
		t.Chdir(t.TempDir())

		adminAddr := randomAddr(t)
		start(t, "--addr", randomAddr(t), "--grpc-addr", randomAddr(t), "--grpc-descriptors", set,
			"--grpc-root-dir", "root", "--grpc-admin-addr", adminAddr, "--log-level", "disabled")
		postGRPCMock(t, adminAddr, "worked", "kept.health.v1")

		require.FileExists(t, filepath.Join("testdata", "vlatte", "_grpc_admin", "worked.mock.yaml"))
	})

	t.Run("from the gRPC root directory", func(t *testing.T) {
		// A relative directory without {workdir} starts at grpc.root_dir, as the gRPC mock files do.
		t.Chdir(t.TempDir())

		adminAddr := randomAddr(t)
		start(t, "--addr", randomAddr(t), "--grpc-addr", randomAddr(t), "--grpc-descriptors", set,
			"--grpc-root-dir", "root", "--grpc-admin-dir", "admin", "--grpc-admin-addr", adminAddr, "--log-level", "disabled")
		postGRPCMock(t, adminAddr, "rooted", "kept.health.v1")

		require.FileExists(t, filepath.Join("root", "admin", "rooted.mock.yaml"))
	})
}

func TestRun_AKeptGRPCMockOfAMethodNoLongerServedFailsTheRun(t *testing.T) {
	// A kept mock whose method the descriptor sets no longer hold would fail every start: the run names its file, so
	// the user can remove it.
	withoutDockerHost(t)

	set, _ := healthSet(t, "stale.health.v1")
	t.Chdir(t.TempDir())

	dir := filepath.Join("testdata", "vlatte", "_grpc_admin")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stale.mock.yaml"), []byte(`
id: stale
request:
  method: removed.v1.Service/Method
response:
  message: {}
`), 0o600))

	// A done context ends a run that started at once.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{
		"--addr", randomAddr(t), "--grpc-addr", randomAddr(t), "--grpc-descriptors", set, "--log-level", "disabled",
	}, nil, io.Discard, &stderr)

	require.Equal(t, 1, code, stderr.String())
	require.Contains(t, stderr.String(), "stale.mock.yaml")
}

// postGRPCMock adds the mock id, of the Check method of the health service in package pkg, which answers SERVING,
// through the gRPC admin API on addr, once it serves.
func postGRPCMock(t *testing.T, addr, id, pkg string) {
	t.Helper()

	doc := fmt.Sprintf("id: %s\nrequest: {method: %s.Health/Check}\nresponse: {message: {status: SERVING}}\n", id, pkg)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Post("http://"+addr+"/mocks", "application/yaml", strings.NewReader(doc))
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, http.StatusCreated, res.StatusCode)
	}, 10*time.Second, 50*time.Millisecond)
}

// requireServing requires the gRPC server on addr to answer SERVING to the Check method of the health service of
// file, in package pkg, once it serves.
func requireServing(t *testing.T, addr string, file protoreflect.FileDescriptor, pkg string) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	msgs := file.Messages()
	req := dynamicpb.NewMessage(msgs.ByName("HealthCheckRequest"))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res := dynamicpb.NewMessage(msgs.ByName("HealthCheckResponse"))
		if !assert.NoError(c, conn.Invoke(t.Context(), "/"+pkg+".Health/Check", req, res)) {
			return
		}

		status := res.Descriptor().Fields().ByName("status")
		assert.Equal(c, status.Enum().Values().ByName("SERVING").Number(), res.Get(status).Enum())
	}, 10*time.Second, 100*time.Millisecond)
}

func TestRun_AGRPCAdminDirOfAWorkingDirectoryThatIsGoneFailsTheRun(t *testing.T) {
	// A {workdir} admin directory is made absolute from the working directory: without one, the run fails with the
	// setting's name instead of keeping the mocks somewhere else.
	withoutDockerHost(t)

	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.Remove(dir))

	if _, err := os.Getwd(); err == nil {
		t.Skip("the OS still names a working directory that is gone, as macOS does")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{"--addr", randomAddr(t), "--journal=false", "--log-level", "disabled"}, nil, io.Discard, &stderr)

	require.Equal(t, 1, code, stderr.String())
	require.Contains(t, stderr.String(), "config: grpc.admin.dir: ")
}
