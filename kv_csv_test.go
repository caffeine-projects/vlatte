package vlatte_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/kvtest"
)

func TestCSVKV_KeepsTheKVContract(t *testing.T) {
	kvtest.Run(t, func(t *testing.T) vlatte.KV {
		return vlatte.MustNewCSVKV(writeFile(t, "kv.csv", "key,value\n"))
	})
}

func TestCSVKV_LoadsWhatSpreadsheetsAndEditorsWrite(t *testing.T) {
	// Excel's "CSV UTF-8" starts with a byte order mark and ends lines with CRLF. Quoted fields hold commas, quotes and
	// line breaks, a CRLF in one reading as LF, as encoding/csv reads it. Keys and values keep their spaces.
	file := writeFile(t, "kv.csv", "\uFEFFkey,value\r\n"+
		"token,abc123\r\n"+
		"greeting,\"Hello, \"\"world\"\"\"\r\n"+
		"banner,\"line one\r\nline two\"\r\n"+
		"empty,\r\n"+
		" padded , spaced \r\n")

	kv, err := vlatte.NewCSVKV(file)
	require.NoError(t, err)

	want := map[string]string{
		"token":    "abc123",
		"greeting": `Hello, "world"`,
		"banner":   "line one\nline two",
		"empty":    "",
		" padded ": " spaced ",
	}

	keys, err := kv.Keys(t.Context())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"token", "greeting", "banner", "empty", " padded "}, keys)

	for k, v := range want {
		got, err := kv.Get(t.Context(), k)
		require.NoError(t, err, k)
		require.Equal(t, v, string(got), k)
	}
}

func TestCSVKV_ABrokenFileNamesTheLineToFix(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "no header",
			content: "name,token\nana,abc\n",
			want:    `kv: kv.csv:1: expected the header key,value, not "name,token"`,
		},
		{
			name:    "a header after blank lines",
			content: "\n\nname,token\n",
			want:    `kv: kv.csv:3: expected the header key,value, not "name,token"`,
		},
		{
			name:    "an empty file",
			content: "",
			want:    "kv: kv.csv:1: expected the header key,value, not an empty file",
		},
		{
			name:    "a row without two fields",
			content: "key,value\ntoken,abc\nname\n",
			want:    "kv: kv.csv:3: expected 2 fields, a key and its value, not 1",
		},
		{
			name:    "a row with a comma left unquoted",
			content: "key,value\ngreeting,Hello, world\n",
			want:    "kv: kv.csv:2: expected 2 fields, a key and its value, not 3",
		},
		{
			name:    "a header without two fields",
			content: "key\n",
			want:    `kv: kv.csv:1: expected the header key,value, not "key"`,
		},
		{
			name:    "an empty key",
			content: "key,value\ntoken,abc\n,orphan\n",
			want:    "kv: kv.csv:3: expected a key, not an empty field",
		},
		{
			name:    "a repeated key",
			content: "key,value\ntoken,abc\nname,ana\ntoken,def\n",
			want:    `kv: kv.csv:4: key "token" repeats line 2: keep one of them`,
		},
		{
			name:    "a repeated key after a value with a line break",
			content: "key,value\nbanner,\"one\ntwo\"\nbanner,three\n",
			want:    `kv: kv.csv:4: key "banner" repeats line 2: keep one of them`,
		},
		{
			name:    "a quote that doesn't close",
			content: "key,value\ntoken,\"abc\n",
			want:    `kv: reading kv.csv: parse error on line 2, column 12: extraneous or missing " in quoted-field`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile("kv.csv", []byte(tc.content), 0o600))

			_, err := vlatte.NewCSVKV("kv.csv")
			require.EqualError(t, err, tc.want)
			require.PanicsWithError(t, tc.want, func() { vlatte.MustNewCSVKV("kv.csv") })
		})
	}
}

func TestCSVKV_AFileThatIsNotThereFails(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := vlatte.NewCSVKV("kv.csv")
	require.ErrorIs(t, err, os.ErrNotExist)
	require.EqualError(t, err, "kv: reading kv.csv: open kv.csv: no such file or directory")
}

func TestCSVKV_NeverWritesTheFile(t *testing.T) {
	// The file is the user's: Set and Del change the store, as the admin API changes the mocks, never their files.
	content := "key,value\ntoken,abc123\nname,ana\n"
	file := writeFile(t, "kv.csv", content)
	kv := vlatte.MustNewCSVKV(file)

	require.NoError(t, kv.Set(t.Context(), "token", []byte("def456")))
	require.NoError(t, kv.Set(t.Context(), "city", []byte("rio")))
	require.NoError(t, kv.Del(t.Context(), "name"))

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "def456", string(got))

	b, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, content, string(b))
}

// writeFile writes content to a file named name in a directory of t's, and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}
