package mockhttp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// discardWriter answers a request into nothing, so a benchmark measures the server alone.
type discardWriter struct {
	header http.Header
}

func (w *discardWriter) Header() http.Header         { return w.header }
func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardWriter) WriteHeader(int)             {}

// benchHandler returns the handler of a server, which it serves without listening.
func benchHandler(b *testing.B, mock *Builder) (*Server, http.Handler) {
	b.Helper()

	var handler http.Handler

	m := MustNew(Setup().Silent().HandlerDecorator(func(h http.Handler) http.Handler {
		handler = h
		return h
	}))
	b.Cleanup(m.Close)
	m.MustMock(mock)

	return m, handler
}

func BenchmarkServeHTTP_Recording(b *testing.B) {
	// What a recording adds to a request: a small answer, recorded or not.
	for _, recording := range []bool{false, true} {
		b.Run(fmt.Sprintf("recording=%v", recording), func(b *testing.B) {
			m, handler := benchHandler(b, Getf("/items").Reply(OK().PlainText("a small answer")))
			if recording {
				require.NoError(b, m.StartRecording(RecordDir(b.TempDir()), RecordIncludeMocked(true)))
			}

			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(&discardWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/items?page=2", nil))
			}
		})
	}
}

func BenchmarkRecording_LargeBody(b *testing.B) {
	// A large body goes to a file as it is captured: the memory a call takes doesn't grow with its body.
	for _, size := range []int{1 << 20, 8 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			body := bytes.Repeat([]byte("0123456789abcdef"), size/16)

			m, handler := benchHandler(b, Getf("/big").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
				// As a proxy copies a target's body.
				_, _ = io.CopyBuffer(w, struct{ io.Reader }{bytes.NewReader(body)}, make([]byte, 32<<10))
			})))

			require.NoError(b, m.StartRecording(RecordDir(b.TempDir()), RecordIncludeMocked(true)))

			b.SetBytes(int64(size))
			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(&discardWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/big", nil))
			}

			b.StopTimer()

			// A body the disk falls behind isn't recorded: the call fails, and the answer goes on.
			res, _ := m.StopRecording(b.Context())
			require.NotNil(b, res)
			b.ReportMetric(float64(res.Status.Failed)/float64(res.Status.Calls), "failed/op")
		})
	}
}
