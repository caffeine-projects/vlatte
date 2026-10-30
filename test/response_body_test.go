package test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestBinaryReplies_PreserveEveryByteAcrossRequests(t *testing.T) {
	// Mocha #61: scanning response bodies as lines removed delimiters and cut long binary payloads.
	palette := make([]byte, 256)
	for i := range palette {
		palette[i] = byte(i)
	}

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, err := gz.Write(bytes.Repeat(palette, 1024))
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	tests := []struct {
		name string
		body []byte
	}{
		{name: "all byte values including line endings", body: bytes.Repeat(palette, 1024)},
		{name: "over 64 KiB without a newline", body: bytes.Repeat([]byte{0xff}, 128<<10)},
		{name: "gzip artifact", body: compressed.Bytes()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "artifact.bin")
			require.NoError(t, os.WriteFile(file, tt.body, 0o600))

			m := mockhttp.MustNew(mockhttp.Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(mockhttp.Getf("/bytes").Reply(mockhttp.OK().Body(tt.body)))
			m.MustMock(mockhttp.Getf("/reader").Reply(mockhttp.OK().BodyReader(bytes.NewReader(tt.body))))
			m.MustMock(mockhttp.Getf("/file").Reply(mockhttp.OK().BodyFile(file)))

			for _, path := range []string{"/bytes", "/reader", "/file"} {
				t.Run(path, func(t *testing.T) {
					req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL(path), nil)
					require.NoError(t, err)
					for range 2 {
						res, body := do(t, req)
						require.Equal(t, http.StatusOK, res.StatusCode)
						require.Equal(t, tt.body, body)
					}
				})
			}
		})
	}
}
