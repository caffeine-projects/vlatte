package bodyfile

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	// root/mocks holds the mock and mocks/sub a directory of its files; root/out.txt is outside both, and
	// mocks/link.txt points to it.
	root := t.TempDir()
	dir := filepath.Join(root, "mocks")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("in"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "in.txt"), []byte("sub"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "out.txt"), []byte("out"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "out.txt"), filepath.Join(dir, "link.txt")))

	outside := filepath.Join(root, "out.txt")

	tests := []struct {
		name   string
		source string
		file   string
		want   string // the content; "" means the file must not open
	}{
		{name: "a written relative name starts at the dir", source: "in.txt", file: "in.txt", want: "in"},
		{name: "a written name may leave the dir", source: "../out.txt", file: "../out.txt", want: "out"},
		{name: "a written absolute name opens as it is", source: outside, file: outside, want: "out"},
		{name: "a written link may point out", source: "link.txt", file: "link.txt", want: "out"},
		{name: "a rendered name opens inside the dir", source: "{{ .Name }}", file: "in.txt", want: "in"},
		{name: "a rendered name can't leave the dir", source: "{{ .Name }}", file: "../out.txt"},
		{name: "a rendered absolute name is refused", source: "{{ .Name }}", file: outside},
		{name: "a rendered name can't follow a link out", source: "{{ .Name }}", file: "link.txt"},
		{name: "a rendered name opens in its written directory", source: "sub/{{ .Name }}", file: "sub/in.txt", want: "sub"},
		{name: "a rendered name can't leave its written directory", source: "sub/{{ .Name }}", file: "sub/../in.txt"},
		{name: "a written absolute directory holds a rendered name", source: filepath.Join(root, "{{ .Name }}"), file: outside, want: "out"},
		// A template copies the text before its first action, so a rendered name starts with what was written.
		{name: "a rendered name can't climb out of a written absolute directory", source: filepath.Join(dir, "{{ .Name }}"), file: dir + string(filepath.Separator) + filepath.Join("..", "out.txt")},
		{name: "a written .. is the author's", source: "../{{ .Name }}", file: "../out.txt", want: "out"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Open(dir, tc.source, tc.file)
			if tc.want == "" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			defer f.Close()

			b, err := io.ReadAll(f)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(b))
		})
	}
}

func TestOpen_EmptyDirIsTheWorkingDir(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("in.txt", []byte("in"), 0o600))

	for _, source := range []string{"in.txt", "{{ .Name }}"} {
		f, err := Open("", source, "in.txt")
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}

	_, err := Open("", "{{ .Name }}", "../in.txt")
	require.Error(t, err)
}
