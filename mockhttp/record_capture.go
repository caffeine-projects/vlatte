package mockhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ http.ResponseWriter = (*captureWriter)(nil)
	_ http.Flusher        = (*captureWriter)(nil)
	_ http.Hijacker       = (*captureWriter)(nil)
	_ io.ReaderFrom       = (*captureWriter)(nil)
)

// _copyBuffers recycles the buffers bodies are copied through.
var _copyBuffers = sync.Pool{New: func() any {
	b := make([]byte, 32<<10)
	return &b
}}

// writerOnly hides the io.ReaderFrom of a writer, which io.CopyBuffer would call back.
type writerOnly struct {
	io.Writer
}

// requestBodyMode is what a recording can match of a request body.
type requestBodyMode int

const (
	// bodyUnmatched is a body a recording can't match, or a request without one, such as a GET.
	bodyUnmatched requestBodyMode = iota

	// bodyEmpty is a request that could have a body, and came without one.
	bodyEmpty

	// bodyKept is a body a recording matches, kept for it.
	bodyKept
)

// recordedCall is a call the server captured for a recording: the request as it came, before a reply could change
// it, and the response as the client got it.
type recordedCall struct {
	method   string
	url      *url.URL
	header   http.Header
	body     []byte // the request body, when bodyMode is bodyKept
	bodyMode requestBodyMode
	bodySkip string // why the request body isn't matched, when it is worth saying

	status    int
	resHeader http.Header
	trailer   http.Header
	resBody   *vlatte.RecordBody

	// The canonical URL, once read.
	canonical    string
	canonicalErr error
	canonicalSet bool
}

// Discard drops the response body: the call isn't recorded.
func (x *recordedCall) Discard() {
	x.resBody.Discard()
}

// target returns the URL of the request as recordings write it.
func (x *recordedCall) target() (string, error) {
	if !x.canonicalSet {
		x.canonical, x.canonicalErr = canonicalURL(x.url)
		x.canonicalSet = true
	}

	return x.canonical, x.canonicalErr
}

// size is about how many bytes the call holds in memory.
func (x *recordedCall) size() int {
	n := len(x.body) + headerSize(x.header) + headerSize(x.resHeader) + headerSize(x.trailer)
	if b, ok := x.resBody.Bytes(); ok {
		n += len(b)
	}

	return n
}

// captureWriter passes a response to the client, and keeps what the client gets for a recording: the status, the
// headers, the body and the trailers. It wraps the server's writer, so it sees what the steps of a mock send, and
// what replies that write their own response send.
type captureWriter struct {
	rw     http.ResponseWriter
	rec    *recording
	call   *recordedCall
	before http.Header // the headers set before the handler ran, such as CORS headers

	wrote    bool // the status and headers are sent
	head     bool // a HEAD: its answer has no body, whatever the reply writes
	keep     bool // keeps the body
	proxied  bool // the answer is a proxy's
	failed   bool // a write to the client failed
	hijacked bool
	done     bool // the call went to the recording, or was discarded
}

// capture captures the call of rv for the recording, and returns the writer to answer it with. raw is the request
// body the server read, and parseErr why it couldn't parse it.
func (rec *recording) capture(w http.ResponseWriter, rv *RequestValues, raw []byte, parseErr error) *captureWriter {
	r := rv.RawRequest

	call := &recordedCall{
		method:  r.Method,
		url:     rv.URL.Clone(),
		header:  r.Header.Clone(),
		resBody: rec.session.NewBody(),
	}

	call.bodyMode, call.bodySkip = rec.requestBody(rv, raw, parseErr)
	if call.bodyMode == bodyKept {
		// A copy: the server's is the request's, which mappers and callbacks can change.
		call.body = bytes.Clone(raw)
	}

	// The body is kept once the answer is known to be recorded: a mock's is only when the recording asks for them.
	head := r.Method == http.MethodHead

	return &captureWriter{rw: w, rec: rec, call: call, before: w.Header().Clone(), head: head, keep: !head && rec.conf.IncludeMocked}
}

// requestBody returns what a recording can match of the body of rv, and why it can't, when it can't.
func (rec *recording) requestBody(rv *RequestValues, raw []byte, parseErr error) (requestBodyMode, string) {
	r := rv.RawRequest
	conf := rv.App.config

	// The server reads no body then: the recording doesn't either.
	if conf.NoBodyParsing || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return bodyUnmatched, ""
	}

	// The server read only part of it. A body exactly at the limit may have had more.
	if limit := conf.MaxBodyParsingLimit; limit > 0 && int64(len(raw)) >= limit || r.ContentLength > int64(len(raw)) {
		return bodyUnmatched, "the server read only part of the body"
	}

	switch {
	case parseErr != nil:
		return bodyUnmatched, "the body doesn't parse"
	case len(raw) == 0:
		return bodyEmpty, ""
	case !builtinParser(rv.bodyParser):
		return bodyUnmatched, "a parser of the server's config read the body"
	case int64(len(raw)) > rec.codec.maxInline:
		return bodyUnmatched, "the body is larger than MaxInlineBody"
	default:
		return bodyKept, ""
	}
}

// Header implements http.ResponseWriter.
func (c *captureWriter) Header() http.Header {
	return c.rw.Header()
}

// WriteHeader implements http.ResponseWriter. An informational status, such as 103 Early Hints, isn't the answer.
func (c *captureWriter) WriteHeader(code int) {
	if !c.wrote && (code < 100 || code > 199 || code == http.StatusSwitchingProtocols) {
		c.start(code)
	}

	c.rw.WriteHeader(code)
}

// Write implements http.ResponseWriter. The client gets p first.
func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.wrote {
		c.start(http.StatusOK)
	}

	n, err := c.rw.Write(p)

	// A status without a body, such as 204, refuses the body: the client got the answer all the same.
	if err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		c.failed = true
	}

	if c.keep {
		c.call.resBody.Keep(p[:n])
	}

	return n, err
}

// Flush implements http.Flusher.
func (c *captureWriter) Flush() {
	_ = c.FlushError()
}

// FlushError flushes the response to the client, as http.ResponseController does. A flush sends the status and
// headers.
func (c *captureWriter) FlushError() error {
	if !c.wrote {
		c.start(http.StatusOK)
	}

	return http.NewResponseController(c.rw).Flush()
}

// Hijack implements http.Hijacker. A connection taken over, such as by an upgrade, isn't recorded.
func (c *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c.hijacked = true
	c.drop()

	return http.NewResponseController(c.rw).Hijack()
}

// ReadFrom implements io.ReaderFrom, so copying a body, such as a proxy's, reuses a buffer.
func (c *captureWriter) ReadFrom(r io.Reader) (int64, error) {
	buf, _ := _copyBuffers.Get().(*[]byte)
	defer _copyBuffers.Put(buf)

	return io.CopyBuffer(writerOnly{c}, r, *buf)
}

// Unwrap returns the response writer, so http.ResponseController reaches it.
func (c *captureWriter) Unwrap() http.ResponseWriter {
	return c.rw
}

// answeredBy tells who answers: a proxy, or a mock. A mock's answer is recorded only with
// RecordConfig.IncludeMocked, so its body isn't kept otherwise.
func (c *captureWriter) answeredBy(proxied bool) {
	c.proxied = proxied

	switch {
	case proxied:
		c.keep = !c.head
	case !c.rec.conf.IncludeMocked:
		c.drop()
	}
}

// record hands the call to the recording, once the client got the whole answer.
func (c *captureWriter) record() {
	if c.done {
		return
	}

	if c.hijacked || c.failed || !c.keepsAnswer() {
		c.end()
		return
	}

	c.done = true

	// A handler that writes nothing answers 200.
	if !c.wrote {
		c.start(http.StatusOK)
	}

	c.call.trailer = trailers(c.call.resHeader, c.rw.Header())
	c.call.resBody.Close()
	c.rec.session.Record(c.call, c.call.size())
}

// end discards the call, unless it went to the recording.
func (c *captureWriter) end() {
	if !c.done {
		c.done = true
		c.call.Discard()
	}
}

// keepsAnswer reports whether the recording takes the answer: a proxy's, or a mock's when it asks for them.
func (c *captureWriter) keepsAnswer() bool {
	return c.proxied || c.rec.conf.IncludeMocked
}

// drop stops keeping the body: the call won't be recorded.
func (c *captureWriter) drop() {
	c.keep = false
	c.call.resBody.Discard()
}

// start keeps the status and the headers the answer has, when they are sent. The headers set before the handler ran,
// such as by CORS, aren't the answer's: only the values the handler added to them are kept.
func (c *captureWriter) start(code int) {
	c.wrote = true
	c.call.status = code

	h := c.rw.Header()
	c.call.resHeader = make(http.Header, len(h))

	for k, values := range h {
		if before, ok := c.before[k]; ok && len(values) >= len(before) && slices.Equal(values[:len(before)], before) {
			values = values[len(before):]
		}

		if len(values) > 0 {
			c.call.resHeader[k] = slices.Clone(values)
		}
	}
}

// trailers returns the trailers of an answer whose headers were header, from what the writer holds once the body is
// sent: the trailers header announced, and those named with http.TrailerPrefix.
func trailers(header, after http.Header) http.Header {
	var out http.Header

	add := func(name string, values []string) {
		if len(values) == 0 {
			return
		}

		if out == nil {
			out = make(http.Header)
		}

		out[http.CanonicalHeaderKey(name)] = append(out[http.CanonicalHeaderKey(name)], values...)
	}

	for _, v := range header.Values(HeaderTrailer) {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				add(name, after.Values(name))
			}
		}
	}

	for k, values := range after {
		if name, ok := strings.CutPrefix(k, http.TrailerPrefix); ok {
			add(name, values)
		}
	}

	return out
}

// builtinParser reports whether p is one of the parsers the server comes with, whose readings a recording knows.
func builtinParser(p RequestBodyParser) bool {
	switch p.(type) {
	case *plainTextParser, *formURLEncodedParser, *multipartFormParser, *noopParser:
		return true
	default:
		return false
	}
}

// headerSize is about how many bytes h holds.
func headerSize(h http.Header) int {
	n := 0
	for k, values := range h {
		n += len(k)
		for _, v := range values {
			n += len(v)
		}
	}

	return n
}
