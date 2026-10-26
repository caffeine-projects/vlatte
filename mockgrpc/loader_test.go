package mockgrpc

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestMockFiles_LoadAtStartAndOnReload(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "greet.yaml", `{request: {method: Greetings}, response: {message: {message: v1}}}`)

	m := fileServer(t, Setup().RootDir(dir).MockFiles("*.yaml"))
	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "v1", got)

	writeFile(t, dir, "greet.yaml", `{request: {method: Greetings}, response: {message: {message: v2}}}`)
	require.NoError(t, m.Reload())

	got, _ = greetWith(t, c, "hi")
	require.Equal(t, "v2", got, "a reload swaps the mocks of the files")

	writeFile(t, dir, "greet.yaml", `{request: {method: Nope}, response: {}}`)
	require.Error(t, m.Reload())

	got, _ = greetWith(t, c, "hi")
	require.Equal(t, "v2", got, "a reload that fails changes nothing")

	require.NoError(t, os.Remove(file))
	require.NoError(t, m.Reload())

	_, code := greetWith(t, c, "hi")
	require.Equal(t, codes.NotFound, code, "a removed file's mock goes")
}

func TestMockFiles_AStartThatFailsToLoadThemServesNothing(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.yaml", `{request: {method: Nope}, response: {}}`)
	m := MustNew(Setup().Silent().MockFiles(path))

	require.ErrorContains(t, m.Start(), "unknown method")
	require.Empty(t, m.Addr(), "nothing listens")
}
