package mockhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender_AFlushedBodyStopsAtTheFirstFailure(t *testing.T) {
	// A streamed body, such as an event stream, gzipped or not, stops at the first write or flush that fails, and
	// says why. The headers are flushed first.
	tests := []struct {
		name     string
		encoding string
		w        *failingWriter
		want     string
	}{
		{"the headers", "", &failingWriter{failFlush: 1}, "flush failed"},
		{"a write", "", &failingWriter{failWrite: 1}, "broken pipe"},
		{"a flush", "", &failingWriter{failFlush: 2}, "flush failed"},
		{"the gzipped headers", "gzip", &failingWriter{failFlush: 1}, "flush failed"},
		{"the gzip header", "gzip", &failingWriter{failWrite: 1}, "broken pipe"},
		{"a gzipped flush", "gzip", &failingWriter{failFlush: 2}, "flush failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.w.header = make(http.Header)
			res := &Response{StatusCode: http.StatusOK, Header: make(http.Header), BodyCloser: io.NopCloser(strings.NewReader("data: a\n\n")), Encoding: tc.encoding, flush: true}
			require.EqualError(t, res.Render(t.Context(), tc.w), tc.want)
		})
	}
}

// leavingBody is a streamed body whose client leaves once it has read the body: the next read cancels the context,
// as a client that goes away does, and ends the body, as a stream that keeps open does then.
type leavingBody struct {
	r     io.Reader
	leave context.CancelFunc
}

func (b *leavingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		b.leave()
	}

	return n, err
}

func (b *leavingBody) Close() error { return nil }

// goneWriter is the response writer of a client that may have left: once ctx ends, writes fail as they do on a
// closed connection.
type goneWriter struct {
	ctx    context.Context
	header http.Header
}

func (w *goneWriter) Header() http.Header { return w.header }
func (w *goneWriter) WriteHeader(int)     {}

func (w *goneWriter) Write(p []byte) (int, error) {
	if w.ctx.Err() != nil {
		return 0, errors.New("use of closed network connection")
	}

	return len(p), nil
}

func TestRender_AClientLeavingAFlushedBodyIsNoFailure(t *testing.T) {
	// A stream that keeps open ends when its client leaves, which is how such a stream ends: the end of a gzip stream
	// has no one to go to then, and that isn't a failure to report.
	for _, encoding := range []string{"", "gzip"} {
		t.Run("encoding "+encoding, func(t *testing.T) {
			ctx, leave := context.WithCancel(t.Context())
			defer leave()

			res := &Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				BodyCloser: &leavingBody{r: strings.NewReader("data: a\n\n"), leave: leave},
				Encoding:   encoding,
				flush:      true,
			}

			require.NoError(t, res.Render(ctx, &goneWriter{ctx: ctx, header: make(http.Header)}))
		})
	}
}
