package mockhttp

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// _idOracle is the ID rule written independently of checkMockID: the characters a URL path needs no escaping for.
var _idOracle = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

func TestBuilder_ID(t *testing.T) {
	m := MustNew(Setup().Silent())

	mock, err := Getf("/users").ID("users-get").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "users-get", mock.ID())

	_, err = Getf("/users").ID("users/get").Reply(OK()).Build(m)
	require.ErrorIs(t, err, errMockID)
	require.EqualError(t, err, `mock: id "users/get": an id holds only letters, digits, '.', '_', '~' and '-'`)

	generated, err := Getf("/users").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Regexp(t, _idOracle, generated.ID(), "generated IDs follow the rule too")
}

func TestPropBuilder_IDRule(t *testing.T) {
	// M7: an ID is accepted iff it follows the rule, and it is kept exactly as given.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.OneOf(rapid.StringMatching(`[A-Za-z0-9._~-]{0,12}`), rapid.String()).Draw(t, "id")

		mock, err := Getf("/x").ID(id).Reply(OK()).Build(m)
		if _idOracle.MatchString(id) {
			require.NoError(t, err)
			require.Equal(t, id, mock.ID())
		} else {
			require.ErrorIs(t, err, errMockID)
		}
	})
}

func TestReply_BodyFileDir(t *testing.T) {
	// A relative body file opens from the reply's dir; an absolute one, and one without a dir, as given.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "body.txt"), []byte("from dir"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "users.txt"), []byte("templated"), 0o600))

	inDir := func(rep *Reply) *Reply {
		rep.bodyFileDir = dir
		return rep
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/relative").Reply(inDir(OK().BodyFile("body.txt"))),
		Getf("/absolute").Reply(inDir(OK().BodyFile(filepath.Join(dir, "sub", "users.txt")))),
		Getf("/users").Reply(inDir(OK().BodyFile("sub/{{ index .Request.URLPathSegments 0 }}.txt"))),
		Getf("/working-dir").Reply(OK().BodyFile("testdata/loader/02.txt")),
	)

	for path, want := range map[string]string{
		"/relative":    "from dir",
		"/absolute":    "templated",
		"/users":       "templated",
		"/working-dir": "test ok\n",
	} {
		res, err := http.Get(m.URL(path))
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, want, string(b), path)
	}
}

func TestReply_TemplatedBodyKeepsItsBytes(t *testing.T) {
	// Found by TestPropMockFile_ServesItsModel: a templated body pointed into a pooled buffer that went back
	// to the pool before the response rendered, so gzip, which takes a pooled buffer too, wrote over it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/gzip").Reply(OK().BodyTemplate("{{ .Request.Method }} templated").Gzip()),
		Getf("/file").Reply(OK().BodyFileWithTemplate("testdata/loader/02.txt").Gzip()),
	)

	for path, want := range map[string]string{"/gzip": "GET templated", "/file": "test ok\n"} {
		for range 20 {
			res, err := http.Get(m.URL(path))
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, want, string(b), path)
		}
	}
}
