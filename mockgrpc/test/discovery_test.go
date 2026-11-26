package test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

const _discoveryFixture = "testdata/vlatte/_mocks_grpc/greetings.mock.yaml"

func TestDiscovery_FileAndGoMocksSurviveReload(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VLATTE_CONFIG", "")
	t.Setenv("VLATTE_GRPC_MOCK_FILES", "")
	t.Setenv("VLATTE_GRPC_ROOT_DIR", "")
	writeDiscoveryFile(t, ".vlatterc.yaml", "grpc: {name: discovery}")
	root := t.TempDir()
	path := filepath.Join(root, _discoveryFixture)
	writeDiscoveryFile(t, path, discoveryReply("fixture-v1"))
	writeDiscoveryFile(t, filepath.Join(root, "testdata/vlatte/_mocks/http.mock.yaml"), "malformed: [")
	writeDiscoveryFile(t, filepath.Join(root, "testdata/vlatte/_mocks_grpc/nested/unused.mock.yaml"), "malformed: [")
	writeDiscoveryFile(t, filepath.Join(root, "testdata/vlatte/_mocks_grpc/notes.yaml"), "malformed: [")

	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory().RootDir(root))
	t.Cleanup(m.Close)
	m.MustStart()
	m.MustMock(mockgrpc.Method("Greetings").Field("message", match.Equal("go")).Reply(
		mockgrpc.Message(&pb.HiRes{Message: "from Go"}),
	))
	client := discoveryClient(t, m)

	requireGreeting(t, client, "fixture", "fixture-v1")
	requireGreeting(t, client, "go", "from Go")

	writeDiscoveryFile(t, ".vlatterc.yaml", "grpc: [")
	writeDiscoveryFile(t, path, discoveryReply("fixture-v2"))
	require.NoError(t, m.Reload(), "reload only reads mock files")
	requireGreeting(t, client, "fixture", "fixture-v2")
	requireGreeting(t, client, "go", "from Go")

	writeDiscoveryFile(t, path, "request: [")
	err := m.Reload()
	requireDiscoveryProblem(t, err, "mock", config.CodeParseError)
	require.ErrorContains(t, err, path)
	requireGreeting(t, client, "fixture", "fixture-v2")
	requireGreeting(t, client, "go", "from Go")
}

func TestSkipDefaults_LoadsOnlySelectedFixtures(t *testing.T) {
	t.Setenv("VLATTE_GRPC_MOCK_FILES", "")
	t.Setenv("VLATTE_GRPC_ROOT_DIR", "")
	t.Run("malformed default config", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("VLATTE_CONFIG", "")
		writeDiscoveryFile(t, ".vlatterc.yaml", "grpc: [")
		_, err := mockgrpc.New(mockgrpc.Setup().InMemory())
		requireDiscoveryProblem(t, err, "", config.CodeParseError)
		require.ErrorContains(t, err, ".vlatterc.yaml")
		requireSelectedFixture(t)
	})

	t.Run("hostile config environment", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("VLATTE_CONFIG", "missing.yaml")
		_, err := mockgrpc.New(mockgrpc.Setup().InMemory())
		requireDiscoveryProblem(t, err, "", config.CodeFileNotFound)
		require.ErrorContains(t, err, "env VLATTE_CONFIG")
		require.ErrorContains(t, err, "missing.yaml")
		requireSelectedFixture(t)
	})

	t.Run("malformed conventional fixture", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("VLATTE_CONFIG", "")
		writeDiscoveryFile(t, _discoveryFixture, "request: [")
		m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
		t.Cleanup(m.Close)
		err := m.Start()
		requireDiscoveryProblem(t, err, "mock", config.CodeParseError)
		require.ErrorContains(t, err, _discoveryFixture)
		requireSelectedFixture(t)
	})
}

func TestDiscovery_ExplicitGoPatternsReplaceConfiguredFixtures(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VLATTE_CONFIG", "")
	t.Setenv("VLATTE_GRPC_MOCK_FILES", "")
	t.Setenv("VLATTE_GRPC_ROOT_DIR", "")
	writeDiscoveryFile(t, ".vlatterc.yaml", "grpc: {mock_files: [configured.yaml]}")
	writeDiscoveryFile(t, "configured.yaml", discoveryReply("configured"))
	writeDiscoveryFile(t, "explicit.yaml", discoveryReply("explicit"))
	writeDiscoveryFile(t, _discoveryFixture, discoveryReply("conventional"))

	configured := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
	t.Cleanup(configured.Close)
	configured.MustStart()
	requireGreeting(t, discoveryClient(t, configured), "fixture", "configured")

	explicit := mockgrpc.MustNew(mockgrpc.Setup().InMemory().MockFilePatterns("explicit.yaml"))
	t.Cleanup(explicit.Close)
	explicit.MustStart()
	requireGreeting(t, discoveryClient(t, explicit), "fixture", "explicit")
}

func TestDiscovery_MissingFixtureDirectoryIsOptional(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VLATTE_CONFIG", "")
	t.Setenv("VLATTE_GRPC_MOCK_FILES", "")
	t.Setenv("VLATTE_GRPC_ROOT_DIR", "")
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
	t.Cleanup(m.Close)
	require.NoError(t, m.Start())
	_, err := discoveryClient(t, m).Greetings(t.Context(), &pb.HiReq{Message: "fixture"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestDiscovery_TreatsRootDirAsLiteralWhenGlobbingFixtures(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "[case]")
	writeDiscoveryFile(t, filepath.Join(root, _discoveryFixture), discoveryReply("literal root"))
	writeDiscoveryFile(t, filepath.Join(dir, "c", _discoveryFixture), discoveryReply("sibling"))

	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory().ConfigSources().RootDir(root))
	t.Cleanup(m.Close)
	m.MustStart()
	requireGreeting(t, discoveryClient(t, m), "fixture", "literal root")
}

func requireSelectedFixture(t *testing.T) {
	t.Helper()

	writeDiscoveryFile(t, "selected.yaml", discoveryReply("selected"))
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory().SkipDefaults().MockFilePatterns("selected.yaml"))
	t.Cleanup(m.Close)
	m.MustStart()
	requireGreeting(t, discoveryClient(t, m), "fixture", "selected")
}

func discoveryReply(response string) string {
	return fmt.Sprintf(`request: {method: Greetings, fields: {message: fixture}}
response: {message: {message: %q}}`, response)
}

func writeDiscoveryFile(t *testing.T, path, contents string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

func discoveryClient(t *testing.T, m *mockgrpc.Server) pb.TestServiceClient {
	t.Helper()

	conn := m.MustDial()
	t.Cleanup(func() { conn.Close() })
	return pb.NewTestServiceClient(conn)
}

func requireGreeting(t *testing.T, client pb.TestServiceClient, message, want string) {
	t.Helper()

	got, err := client.Greetings(t.Context(), &pb.HiReq{Message: message})
	require.NoError(t, err)
	require.Equal(t, want, got.GetMessage())
}

func requireDiscoveryProblem(t *testing.T, err error, scope, code string) {
	t.Helper()

	problem, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, scope, problem.Scope)
	require.Len(t, problem.Problems, 1)
	require.Equal(t, code, problem.Problems[0].Code)
}
