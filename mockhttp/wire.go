package mockhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ http.ResponseWriter = (*wireResponseWriter)(nil)
	_ http.Flusher        = (*wireResponseWriter)(nil)
)

// errWireNeedsHTTP1 is the failure of a mock with wire steps over a connection it can't take over, such as an HTTP/2
// one: it answers in HTTP/1.x on the connection itself.
var errWireNeedsHTTP1 = errors.New("http: wire steps need an HTTP/1.x connection")

// _wireHeaders are the headers wireResponseWriter sets itself: the framing of the answer is its own.
var _wireHeaders = []string{HeaderContentLength, "Transfer-Encoding", HeaderConnection}

// wireResponseWriter answers a request on its connection, which the server took over, in HTTP/1.x, through the flow of
// the wire steps of its mock: their stages see every byte of the answer, the status line, the headers, the framing and
// the body. The answer ends with the connection, so it needs no length. Nothing else writes to the connection, and the
// flow orders the writes of a stage that writes from a goroutine of its own.
type wireResponseWriter struct {
	conn      net.Conn
	tunnels   *tunnels
	base      context.Context    // the context the flow derives from, which the server's close ends
	release   context.CancelFunc // releases base
	flow      *vlatte.Flow
	out       vlatte.Stage // the flow, or the connection once the steps are abandoned
	header    http.Header
	upstream  bool // the upstream steps read the request body, before the connection was taken over
	proto     string
	head      bool // a HEAD: the answer has no body
	chunked   bool // HTTP/1.1: the body is chunked
	body      bool // the status allows a body
	started   bool // the status line and headers are sent
	abandoned bool
	finished  bool
}

// takeOver takes the connection of r over from w, for the wire steps of its mock, and returns the writer that answers
// on it, within ctx, which the server's close ends too: once the connection is taken over, a read on it, such as of
// a switch of protocols, can end the request's context. What is left of the request's body is read first, through
// upSteps, the upstream steps of the mock: net/http forbids the body once the connection is taken over. A stage that
// aborts them returns the cause, before the connection is taken over.
func (h *mockHandler) takeOver(ctx context.Context, w http.ResponseWriter, r *RequestValues, steps, upSteps []vlatte.Step) (*wireResponseWriter, error) {
	req := r.RawRequest
	if req.ProtoMajor != 1 {
		return nil, fmt.Errorf("%w, not %s", errWireNeedsHTTP1, req.Proto)
	}

	upstream, err := bufferBody(ctx, req, upSteps)
	if errors.Is(err, vlatte.ErrAbort) {
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("http: reading the request body: %w", err)
	}

	// The headers set so far, such as the debug headers, are the answer's.
	header := w.Header().Clone()

	conn, err := hijack(w)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errWireNeedsHTTP1, err)
	}

	// The server is closing.
	if !h.app.tunnels.add(conn) {
		_ = conn.Close()
		return nil, fmt.Errorf("http: %w", net.ErrClosed)
	}

	ww := &wireResponseWriter{
		conn:     conn,
		tunnels:  &h.app.tunnels,
		header:   header,
		upstream: upstream,
		proto:    "HTTP/1.0",
		head:     req.Method == http.MethodHead,
	}

	if req.ProtoAtLeast(1, 1) {
		ww.proto = "HTTP/1.1"
		ww.chunked = true
	}

	ww.base, ww.release = h.app.tunnels.bind(context.WithoutCancel(ctx))
	ww.flow = vlatte.NewFlow(ww.base, &vlatte.Sink{W: conn}, steps)
	ww.out = ww.flow

	return ww, nil
}

// bufferBody reads what is left of the body of req into memory, so it can be read once the connection is taken over.
// A body the server didn't read whole, which GetBody can't read again, is still the client's: it is read through
// steps, as the target would read it, so they pace the upload, and it is read again as they passed it on. It returns
// whether the steps read it, and the cause of an abort a stage asks for.
func bufferBody(ctx context.Context, req *http.Request, steps []vlatte.Step) (bool, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return false, nil
	}

	var up *stepReader

	body := req.Body
	if len(steps) > 0 && req.GetBody == nil {
		up = newStepReader(ctx, body, steps)
		body = up
	}

	b, err := io.ReadAll(body)
	_ = body.Close()

	if cause := up.aborted(); cause != nil {
		return false, cause
	}

	if err != nil {
		return false, err
	}

	req.Body = io.NopCloser(bytes.NewReader(b))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }

	return up != nil, nil
}

func (w *wireResponseWriter) Header() http.Header { return w.header }

// WriteHeader sends the status line and the headers. An informational status, such as 103 Early Hints, goes first,
// and the answer comes after it. A second status is ignored, as net/http does.
func (w *wireResponseWriter) WriteHeader(code int) {
	if w.started {
		return
	}

	if code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols {
		_, _ = w.out.Write(w.statusHead(code, w.header))
		return
	}

	w.started = true
	w.body = !w.head && code >= http.StatusOK && code != http.StatusNoContent && code != http.StatusNotModified

	header := w.header.Clone()
	for _, name := range _wireHeaders {
		header.Del(name)
	}

	for name := range header {
		if strings.HasPrefix(name, http.TrailerPrefix) {
			delete(header, name)
		}
	}

	header.Set(HeaderConnection, "close")

	if w.chunked && w.body {
		header.Set("Transfer-Encoding", "chunked")
	}

	if _, ok := header["Date"]; !ok {
		header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}

	_, _ = w.out.Write(w.statusHead(code, header))
}

// Write sends p as the body, after the status line and the headers: 200 unless the reply sent a status. A HEAD drops
// it, as net/http does, and a status without a body refuses it.
func (w *wireResponseWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}

	switch {
	case w.head:
		return len(p), nil
	case !w.body:
		return 0, http.ErrBodyNotAllowed
	case len(p) == 0:
		return 0, nil
	case !w.chunked:
		return w.out.Write(p)
	}

	chunk := fmt.Appendf(nil, "%x\r\n", len(p))
	chunk = append(chunk, p...)
	chunk = append(chunk, "\r\n"...)

	if _, err := w.out.Write(chunk); err != nil {
		return 0, err
	}

	return len(p), nil
}

// FlushError passes the flush through the steps, after the status line and the headers.
func (w *wireResponseWriter) FlushError() error {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}

	return w.out.Flush()
}

// Flush implements http.Flusher, as FlushError does.
func (w *wireResponseWriter) Flush() {
	_ = w.FlushError()
}

// Context returns the flow's context: it ends when the request's does, or a stage aborts the flow.
func (w *wireResponseWriter) Context() context.Context {
	return w.flow.Context()
}

// statusHead returns the status line of code and header, as they go out.
func (w *wireResponseWriter) statusHead(code int, header http.Header) []byte {
	text := http.StatusText(code)
	if text == "" {
		text = "status code " + strconv.Itoa(code)
	}

	var b bytes.Buffer
	_, _ = fmt.Fprintf(&b, "%s %d %s\r\n", w.proto, code, text)
	_ = header.Write(&b)
	b.WriteString("\r\n")

	return b.Bytes()
}

// aborted returns the cause of the flow when a stage aborted it, or nil. A nil w, for a mock without wire steps, has
// none.
func (w *wireResponseWriter) aborted() error {
	if w == nil {
		return nil
	}

	if cause := context.Cause(w.flow.Context()); !w.abandoned && errors.Is(cause, vlatte.ErrAbort) {
		return cause
	}

	return nil
}

// abandon ends the steps, which drop what they hold, for vlatte's answer to a failure: what is written after goes
// to the connection as it is. A nil w, for a mock without wire steps, has none.
func (w *wireResponseWriter) abandon() {
	if w == nil || w.abandoned {
		return
	}

	w.abandoned = true
	w.flow.Abort(errAbandoned)
	_ = w.flow.Close()
	w.out = &vlatte.Sink{W: w.conn}
}

// finish ends the answer and the connection, once. Without a cut, the answer gets its end, the last chunk and the
// trailers, and the steps end; cut, or aborted by a stage, the steps drop what they hold. Then the connection is
// closed, or reset for a cause that matches ErrReset. A nil w, for a mock without wire steps, has none.
func (w *wireResponseWriter) finish(cut error) {
	if w == nil || w.finished {
		return
	}

	w.finished = true

	if cut == nil {
		cut = w.aborted()
	}

	if cut == nil {
		w.end()
		cut = w.aborted()
	}

	w.close(cut)
}

// handOver hands the connection over to a switch of protocols, with the flow of the wire steps, which go on with
// what goes to the client, and base, the context of what goes the other way, which the end of the flow doesn't end:
// the answer no longer finishes them. close ends them, as finish does, with the cause of a cut, if any.
func (w *wireResponseWriter) handOver() (conn net.Conn, base context.Context, flow *vlatte.Flow, closeConn func(cut error)) {
	w.finished = true
	return w.conn, w.base, w.flow, w.close
}

// close ends the flow, and closes the connection, or resets it for a cut that matches ErrReset. The steps of a cut
// drop what they hold.
func (w *wireResponseWriter) close(cut error) {
	defer w.release()
	defer w.tunnels.remove(w.conn)

	if cut != nil {
		w.flow.Abort(cut)
	}

	_ = w.flow.Close()

	if errors.Is(cut, vlatte.ErrReset) {
		resetConn(w.conn)
		return
	}

	_ = w.conn.Close()
}

// end writes the end of the answer, the last chunk and the trailers, and ends the steps: they pass on what they hold.
// A reply that wrote nothing answers 200.
func (w *wireResponseWriter) end() {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}

	if w.chunked && w.body {
		var b bytes.Buffer
		b.WriteString("0\r\n")
		_ = w.trailer().Write(&b)
		b.WriteString("\r\n")

		_, _ = w.out.Write(b.Bytes())
	}

	if !w.abandoned {
		_ = w.flow.Close()
	}
}

// trailer returns the trailers the reply set once its body was written: the headers that Trailer announced, and those
// with http.TrailerPrefix.
func (w *wireResponseWriter) trailer() http.Header {
	trailer := http.Header{}

	for _, names := range w.header.Values("Trailer") {
		for name := range strings.SplitSeq(names, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			if values := w.header.Values(name); len(values) > 0 {
				trailer[name] = values
			}
		}
	}

	for name, values := range w.header {
		if after, ok := strings.CutPrefix(name, http.TrailerPrefix); ok {
			trailer[http.CanonicalHeaderKey(after)] = values
		}
	}

	return trailer
}
