package mockgrpc

import (
	"errors"
	"net/http"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
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

func TestMockFiles_LoadsEveryYAMLDocument(t *testing.T) {
	path := writeFile(t, t.TempDir(), "greet.yaml", `
request: {method: Greetings, fields: {message: hi}}
response: {message: {message: hello}}
---
request: {method: Greetings, fields: {message: bye}}
response: {message: {message: goodbye}}
`)
	m := fileServer(t, Setup().InMemory().MockFiles(path))
	c := pb.NewTestServiceClient(m.MustDial())

	got, code := greetWith(t, c, "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hello", got)

	got, code = greetWith(t, c, "bye")
	require.Equal(t, codes.OK, code, "later documents must load too")
	require.Equal(t, "goodbye", got)
}

func TestMockFiles_EqualPriorityFollowsFileAndDocumentOrder(t *testing.T) {
	dir := t.TempDir()
	first := writeFile(t, dir, "a.yml", `
repeat: 1
request: {method: Greetings}
response: {message: {message: first}}
---
repeat: 1
request: {method: Greetings}
response: {message: {message: second}}
`)
	last := writeFile(t, dir, "b.yaml", `
repeat: 1
request: {method: Greetings}
response: {message: {message: third}}
`)
	m := fileServer(t, Setup().InMemory().MockFiles(last, first, first))
	c := pb.NewTestServiceClient(m.MustDial())

	for _, want := range []string{"first", "second", "third"} {
		got, code := greetWith(t, c, "hi")
		require.Equal(t, codes.OK, code)
		require.Equal(t, want, got)
	}

	_, code := greetWith(t, c, "hi")
	require.Equal(t, codes.NotFound, code, "overlapping patterns must not load a document twice")
}

func TestMockFiles_SkipsEmptyDocumentsAndResolvesEachBodyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "first.json", `{"message":"first"}`)
	writeFile(t, dir, "second.json", `{"message":"second"}`)
	path := writeFile(t, dir, "greet.yaml", `
---
# empty first document
---
request: {method: Greetings, fields: {message: first}}
response: {body_file: first.json}
---
# empty middle document
---
request: {method: Greetings, fields: {message: second}}
response: {body_file: second.json}
---
# empty last document
`)
	m := fileServer(t, Setup().InMemory().MockFiles(path))
	c := pb.NewTestServiceClient(m.MustDial())

	for _, want := range []string{"first", "second"} {
		got, code := greetWith(t, c, want)
		require.Equal(t, codes.OK, code)
		require.Equal(t, want, got, "a body_file opens from its YAML file's directory")
	}
}

func TestMockFiles_AnEmptyYAMLStreamAddsNoMocks(t *testing.T) {
	for _, data := range []string{"", "# comment only\n", "---\n# empty\n---\n...\n"} {
		t.Run(data, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "empty.yaml", data)
			m := fileServer(t, Setup().InMemory().MockFiles(path))
			_, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
			require.Equal(t, codes.NotFound, code)
			require.Len(t, m.ListPending(), 0)
		})
	}
}

func TestMockFiles_AnInvalidLaterDocumentKeepsEveryOldMock(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"malformed", "request: ["},
		{"invalid field", "request: {method: Greetings}\nresponse: {}\nunknown: true"},
		{"unknown method", "request: {method: Nope}\nresponse: {}"},
		{"null", "null"},
		{"scalar", "hello"},
		{"list", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "greet.yaml", `
request: {method: Greetings, fields: {message: first}}
response: {message: {message: old-first}}
---
request: {method: Greetings, fields: {message: second}}
response: {message: {message: old-second}}
`)
			m := fileServer(t, Setup().InMemory().MockFiles(path))
			c := pb.NewTestServiceClient(m.MustDial())
			writeFile(t, dir, "greet.yaml", `
request: {method: Greetings, fields: {message: first}}
response: {message: {message: new-first}}
---
`+tc.doc)

			err := m.Reload()
			_, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.ErrorContains(t, err, path)
			require.ErrorContains(t, err, "document 2")

			for _, message := range []string{"first", "second"} {
				got, code := greetWith(t, c, message)
				require.Equal(t, codes.OK, code)
				require.Equal(t, "old-"+message, got, "a failed reload must preserve the entire old set")
			}
		})
	}
}

func TestMockFiles_DuplicateIDsAcrossDocumentsKeepTheOldSet(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "greet.yaml", `
id: original
request: {method: Greetings}
response: {message: {message: old}}
`)
	m := fileServer(t, Setup().InMemory().MockFiles(path))
	writeFile(t, dir, "greet.yaml", `
id: duplicate
request: {method: Greetings}
response: {message: {message: first}}
---
id: duplicate
request: {method: Greetings}
response: {message: {message: second}}
`)

	require.ErrorIs(t, m.Reload(), vlatte.ErrDuplicateID)
	got, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "old", got)
}

func TestMockFiles_RemovingADocumentRemovesOnlyItsMock(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "greet.yaml", `
request: {method: Greetings, fields: {message: first}}
response: {message: {message: first}}
---
request: {method: Greetings, fields: {message: second}}
response: {message: {message: second}}
`)
	m := fileServer(t, Setup().InMemory().MockFiles(path))
	c := pb.NewTestServiceClient(m.MustDial())
	writeFile(t, dir, "greet.yaml", `
request: {method: Greetings, fields: {message: second}}
response: {message: {message: new-second}}
`)
	require.NoError(t, m.Reload())
	_, code := greetWith(t, c, "first")
	require.Equal(t, codes.NotFound, code)
	got, code := greetWith(t, c, "second")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "new-second", got)

	writeFile(t, dir, "greet.yaml", "---\n# no mocks\n---\n")
	require.NoError(t, m.Reload())
	_, code = greetWith(t, c, "second")
	require.Equal(t, codes.NotFound, code, "an empty stream removes every previously loaded document")
}

func TestMockFiles_ReportsProblemsAcrossDocumentsAndFiles(t *testing.T) {
	dir := t.TempDir()
	invalid := writeFile(t, dir, "invalid.yaml", `
request: {method: Greetings}
response: {}
first_typo: true
---
request: {method: Greetings}
response: {}
second_typo: true
`)
	malformed := writeFile(t, dir, "malformed.yaml", "request: [")
	m := MustNew(Setup().Silent().RootDir(dir).MockFiles("*.yaml"))
	t.Cleanup(m.Close)

	err := m.Start()
	require.ErrorContains(t, err, "file "+invalid+" document 1")
	require.ErrorContains(t, err, "first_typo")
	require.ErrorContains(t, err, "file "+invalid+" document 2")
	require.ErrorContains(t, err, "second_typo")
	require.ErrorContains(t, err, "file "+malformed)
	require.Equal(t, "", m.Addr())
}

func TestMockFiles_ProblemOriginsKeepDocumentNumbersAcrossEmptyDocuments(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.yaml", `
---
# empty first document
---
request: {method: Greetings}
response: {}
unknown: true
---
# empty last document
`)
	m := MustNew(Setup().Silent().MockFiles(path))
	t.Cleanup(m.Close)

	err := m.Start()
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Len(t, cfgErr.Problems, 1)
	require.Equal(t, "file "+path+" document 2", cfgErr.Problems[0].Origin)
	require.Equal(t, "unknown", cfgErr.Problems[0].Key)
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
