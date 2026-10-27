package mockgrpc

import (
	"net/http"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte/admin"
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

func TestLoader_ABadPatternFailsStart(t *testing.T) {
	// A start that fails leaves nothing serving: the admin API stops too.
	m := MustNew(Setup().Silent().Admin(admin.MustNew()).MockFiles("["))
	t.Cleanup(m.Close)

	require.ErrorContains(t, m.Start(), "loader: pattern [")

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.AdminURL()+"/health", nil)
	require.NoError(t, err)

	_, err = http.DefaultClient.Do(r)
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
}

func TestMockFiles_LoadEveryMockOfAMultiDocumentYAMLFile(t *testing.T) {
	// A YAML file can hold several mocks, one per document: none may be dropped.
	dir := t.TempDir()
	writeFile(t, dir, "greet.yaml", `---
request: {method: Greetings, fields: {message: hi}}
response: {message: {message: one}}
---
request: {method: Greetings, fields: {message: hey}}
response: {message: {message: two}}
`)

	m := fileServer(t, Setup().RootDir(dir).MockFiles("*.yaml"))
	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "one", got)

	got, _ = greetWith(t, c, "hey")
	require.Equal(t, "two", got)
}
