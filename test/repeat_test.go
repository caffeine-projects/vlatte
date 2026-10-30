package test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestRepeatedReplies_KeepTheirBodyUntilTheMockIsExhausted(t *testing.T) {
	// Mocha #35: reusing an exhausted reader left the second response without its configured body.
	want := []byte("Hello\nworld\r\n")
	file := filepath.Join(t.TempDir(), "body.txt")
	require.NoError(t, os.WriteFile(file, want, 0o600))

	tests := []struct {
		name  string
		reply *mockhttp.Reply
	}{
		{name: "bytes", reply: mockhttp.OK().Body(want)},
		{name: "text", reply: mockhttp.OK().BodyText(string(want))},
		{name: "reader", reply: mockhttp.OK().BodyReader(strings.NewReader(string(want)))},
		{name: "file", reply: mockhttp.OK().BodyFile(file)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mockhttp.MustNew(mockhttp.Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(mockhttp.Getf("/test").Times(2).Reply(tt.reply))

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/test"), nil)
			require.NoError(t, err)
			for range 2 {
				res, body := do(t, req)
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, want, body)
			}

			res, _ := do(t, req)
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
		})
	}
}
