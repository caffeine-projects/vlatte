package mockhttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/httprec"
)

var (
	_ io.Writer = flushingWriter{}

	gzipper = &sync.Pool{New: func() any { return gzip.NewWriter(nil) }}
)

// keptBody is a streamed body that keeps what it sent: once rendered, the response holds it in Body again, for the
// request log, callbacks and events.
type keptBody interface {
	Kept() ([]byte, bool)
}

// Response defines the HTTP response that will be served once a Mock is matched with an HTTP Request.
type Response struct {
	StatusCode   int
	Header       http.Header
	Trailer      http.Header
	Cookies      []*http.Cookie
	Body         []byte
	BodyCloser   io.ReadCloser
	BodyFilename string
	Encoding     string

	// pauses are where a streamed body waits, such as before the parts of a multipart body with delays.
	pauses []mpart.Pause

	// flush sends each part of BodyCloser to the client as soon as it's read, as a proxy does with a target's body.
	flush bool
}

func newResponse() *Response {
	return &Response{Cookies: make([]*http.Cookie, 0), Header: make(http.Header), Trailer: make(http.Header)}
}

// Gunzip decompresses Gzip body.
func (r *Response) Gunzip() ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}

	defer gz.Close()

	return io.ReadAll(gz)
}

// Render writes the response to w. ctx is the request's: a streamed body stops waiting when it ends.
// An error comes after the status and headers are sent, so it can't change the response.
func (r *Response) Render(ctx context.Context, w http.ResponseWriter) error {
	return r.render(ctx, w, wait)
}

// clone returns a copy of r with a Header, Trailer and Cookies of its own, each cookie a copy too. The Body bytes are
// shared: replace them, don't change them in place.
func (r *Response) clone() Response {
	c := *r
	c.Header = r.Header.Clone()
	c.Trailer = r.Trailer.Clone()
	c.Cookies = slices.Clone(r.Cookies)

	for i, cookie := range c.Cookies {
		if cookie == nil { // a nil cookie renders nothing, so keep it rather than panic
			continue
		}

		own := *cookie
		own.Unparsed = slices.Clone(cookie.Unparsed)
		c.Cookies[i] = &own
	}

	return c
}

// render writes the response to w, and waits with wait where a streamed body pauses.
func (r *Response) render(ctx context.Context, w http.ResponseWriter, wait func(context.Context, time.Duration) error) error {
	// Closed whatever the body sent: a mapper can set Body on a response that came with a BodyCloser. Once rendered,
	// a streamed body that kept what it sent is Body again.
	if body := r.BodyCloser; body != nil {
		defer func() {
			body.Close()
			r.BodyCloser = nil

			if kb, ok := body.(keptBody); ok && len(r.Body) == 0 {
				if b, ok := kb.Kept(); ok {
					r.Body = b
				}
			}
		}()
	}

	for k, vv := range r.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	// A streamed body, such as a proxy's, can bring trailers it didn't announce: they are sent too, below.
	announced := make(map[string]struct{}, len(r.Trailer))
	for k := range r.Trailer {
		w.Header().Add(HeaderTrailer, k)
		announced[k] = struct{}{}
	}

	for _, cookie := range r.Cookies {
		http.SetCookie(w, cookie)
	}

	w.WriteHeader(r.StatusCode)

	if len(r.Body) > 0 && len(r.pauses) > 0 {
		if err := r.stream(ctx, w, wait); err != nil {
			return err
		}
	} else if len(r.Body) > 0 {
		if len(r.Encoding) > 0 {
			buf := bufpool.Get()
			buf.Reset()

			if _, err := r.encode(buf, bytes.NewReader(r.Body)); err != nil {
				return err
			}

			if _, err := buf.WriteTo(w); err != nil {
				return err
			}

			bufpool.Put(buf)
		} else {
			w.Write(r.Body)
		}
	} else if r.BodyCloser != nil {
		if len(r.Encoding) > 0 {
			if _, err := r.encode(w, r.BodyCloser); err != nil {
				return err
			}
		} else {
			out := io.Writer(w)
			if r.flush {
				out = flushingWriter{w: w, rc: http.NewResponseController(w)}
			}

			if _, err := io.Copy(out, r.BodyCloser); err != nil {
				return err
			}
		}
	}

	for k, v := range r.Trailer {
		name := k
		if _, ok := announced[k]; !ok {
			name = http.TrailerPrefix + k
		}

		for _, vv := range v {
			w.Header().Add(name, vv)
		}
	}

	return nil
}

// readBody reads a streamed body, from BodyCloser, into Body, and closes it. The server measures Body as it sends it,
// so a Content-Length that came with the stream goes: whoever reads Body can change it.
func (r *Response) readBody() error {
	if r.BodyCloser == nil {
		return nil
	}

	defer r.BodyCloser.Close()

	b, err := io.ReadAll(r.BodyCloser)
	r.Body, r.BodyCloser = b, nil
	r.Header.Del(HeaderContentLength)

	return err
}

// flushingWriter flushes w after every write, so a streamed body reaches the client as it comes.
type flushingWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		return n, err
	}

	return n, flushTo(f.rc)
}

// flushTo flushes rc, when its writer can flush: one that can't, such as a recorder, sends everything at the end.
func flushTo(rc *http.ResponseController) error {
	if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	return nil
}

// stream writes the body in segments, and waits at each pause before the bytes after it. A segment is flushed before
// the wait, through gzip when the body is encoded, so the client reads it meanwhile, and so is the last one. A pause
// past the end of the body, which a Mapper may shorten, waits at its end.
func (r *Response) stream(ctx context.Context, w http.ResponseWriter, wait func(context.Context, time.Duration) error) error {
	rc := http.NewResponseController(w)

	out := io.Writer(w)

	var gz *gzip.Writer
	if r.Encoding == "gzip" {
		gz = gzipWriter(w)
		defer gzipper.Put(gz)

		out = gz
	}

	flush := func() error {
		if gz != nil {
			if err := gz.Flush(); err != nil {
				return err
			}
		}

		return flushTo(rc)
	}

	written := 0
	for _, p := range r.pauses {
		if at := min(p.At, len(r.Body)); at > written {
			if _, err := out.Write(r.Body[written:at]); err != nil {
				return err
			}

			written = at
		}

		if err := flush(); err != nil {
			return err
		}

		if err := wait(ctx, p.Delay); err != nil {
			return err
		}
	}

	if _, err := out.Write(r.Body[written:]); err != nil {
		return err
	}

	// The last part goes out now, not when the handler returns after callbacks and on_complete events. A failed Close
	// keeps its error, which flush returns.
	if gz != nil {
		_ = gz.Close()
	}

	return flush()
}

// wait waits for d, or until ctx ends.
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (r *Response) encode(w io.Writer, reader io.Reader) (n int64, err error) {
	switch r.Encoding {
	case "gzip":
		gz := gzipWriter(w)

		defer func() {
			gz.Close()
			gzipper.Put(gz)
		}()

		return io.Copy(gz, reader)
	}

	return 0, nil
}

// gzipWriter takes a gzip writer from the pool, which holds only *gzip.Writer, and points it at w.
func gzipWriter(w io.Writer) *gzip.Writer {
	gz, _ := gzipper.Get().(*gzip.Writer)
	gz.Reset(w)

	return gz
}

func responseFromWriter(w http.ResponseWriter) (*Response, error) {
	rw, ok := w.(*httprec.HTTPRec)
	if !ok {
		return nil, fmt.Errorf("mock: response writer is %T, want *httprec.HTTPRec", w)
	}

	result := rw.Result()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}

	defer result.Body.Close()

	if len(w.Header()) != len(result.Header) {
		for k, v := range w.Header() {
			if result.Header.Get(k) == "" {
				for _, vv := range v {
					result.Header.Add(k, vv)
				}
			}
		}
	}

	res := new(Response)
	res.StatusCode = result.StatusCode
	res.Header = result.Header.Clone()
	res.Cookies = result.Cookies()

	if len(body) > 0 {
		res.Body = body
	}

	return res, nil
}
