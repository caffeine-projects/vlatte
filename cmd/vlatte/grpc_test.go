package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
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
		"--debug",
	})

	require.Equal(t, []string{"--addr", ":8080", "-c", "settings.yaml", "--debug"}, httpArgs)
	require.Equal(t, []string{
		"--grpc", "--grpc-addr", ":50051", "--grpc-mock-files=mocks/*.yaml", "--config=settings.yaml",
		"--grpc-reflection=false",
	}, grpcArgs)
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
	// has no code of, as WireMock does with .dsc files, next to the HTTP ones.
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

func TestRun_InvalidGRPCSettingsLeaveNothingOpen(t *testing.T) {
	// An invalid gRPC setting is a usage error. The HTTP server, built first, kept its journal open: the next run in
	// the directory found it in use.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	var stderr bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"--grpc-no-match-code=NOPE"}, io.Discard, &stderr))
	require.Contains(t, stderr.String(), "grpc.no_match_code")

	stop := start(t, "--addr", randomAddr(t))
	require.Equal(t, 0, stop(), "the next run starts and stops")
}
