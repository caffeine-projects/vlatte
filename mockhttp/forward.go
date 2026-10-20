package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	_ io.ReadCloser = (*targetBody)(nil)
	_ keptBody      = (*targetBody)(nil)
)

var (
	// errNoHeaders ends a call to an upstream that didn't send its response headers within the timeout.
	errNoHeaders = errors.New("proxy: no response headers in time")

	// errProxyLoop refuses a request that came back to the server that sent it on.
	errProxyLoop = errors.New("proxy loop: the request came back to this server: its target, or a proxy on the way, is this server")
)

var _forbiddenHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

const (
	// _keptBodyLimit bounds how much of a target's body a proxy keeps, for the request log, callbacks and events,
	// while it streams to the client: a larger body isn't kept.
	_keptBodyLimit = 1 << 20

	// _viaHeader holds the IDs of the servers a request went through. A server that gets back a request it sent on,
	// such as through a proxy reply whose target is the server itself, refuses it rather than send it on forever. It
	// isn't Via, which upstreams read: an nginx with gzip_proxied off doesn't compress its answer to a request with one.
	_viaHeader = "VLatte-Via"

	// _maskedQueryValue stands for the values of a query in the URLs errors and logs show.
	_maskedQueryValue = "xxxxx"
)

// upstream sends requests on to their servers, and answers with the servers' responses, as a proxy does.
type upstream struct {
	rt      http.RoundTripper
	owned   *http.Transport // rt, when the server made it: close releases its idle connections
	timeout time.Duration   // bounds the wait for the response headers, from the call; 0 waits forever
}

// send sends out to its server and returns the server's response, whose body streams to the client as the server
// sends it. The call ends when the body is closed. display names the server in errors.
//
// The response leaves out the hop-by-hop headers, which are about the server's connection to the proxy, and the
// headers of vlatte's, such as the debug headers of a server that is a vlatte server too: the client gets the proxy's.
func (u *upstream) send(out *http.Request, display string) (*Response, error) {
	// The timeout covers the wait for the response headers. The call ends when its body is closed.
	ctx, cancel := context.WithCancelCause(out.Context())
	out = out.WithContext(ctx)

	var timer *time.Timer
	if u.timeout > 0 {
		timer = time.AfterFunc(u.timeout, func() { cancel(errNoHeaders) })
	}

	res, err := u.rt.RoundTrip(out)

	// The timer fired, or is firing: whatever RoundTrip returned, the call is over. Cancelling here, before reading
	// anything, doesn't wait for the timer's own cancel.
	if timer != nil && !timer.Stop() {
		cancel(errNoHeaders)

		if err == nil {
			res.Body.Close()
		}

		return nil, fmt.Errorf("calling the target %s: no response headers within %s", display, u.timeout)
	}

	if err != nil {
		cancel(nil)
		return nil, fmt.Errorf("calling the target %s: %w", display, err)
	}

	removeHopByHop(res.Header)
	maps.DeleteFunc(res.Header, isVlatteHeader)

	stub := newResponse()
	stub.StatusCode = res.StatusCode
	stub.BodyCloser = &targetBody{ReadCloser: res.Body, cancel: cancel}
	stub.proxied = true

	// A body of unknown length, or an event stream, goes to the client part by part, as the server sends it. Other
	// bodies fill the server's buffer, which sends them as it fills.
	stub.flush = res.ContentLength < 0 || mediaType(res.Header.Get(HeaderContentType)) == "text/event-stream"

	// A transport of the caller's may leave the header out.
	if res.Header != nil {
		stub.Header = res.Header
	}

	// The server's trailers: the keys it announced now, and their values once its body is read to the end.
	if res.Trailer != nil {
		stub.Trailer = res.Trailer
	}

	return stub, nil
}

// close releases the idle connections of the transport the server made, if it did. The transport stays usable: a
// request that was being served still gets its answer.
func (u *upstream) close() {
	if u.owned != nil {
		u.owned.CloseIdleConnections()
	}
}

// targetBody is the body of a target's response, which streams to the client. It keeps what it reads, up to
// _keptBodyLimit, and closing it ends the call to the target.
type targetBody struct {
	io.ReadCloser

	cancel context.CancelCauseFunc
	kept   []byte
	over   bool // read more than it keeps
	eof    bool
}

// Read reads the body, and keeps what it read.
func (b *targetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	switch {
	case b.over:
	case len(b.kept)+n > _keptBodyLimit:
		b.over, b.kept = true, nil
	default:
		b.kept = append(b.kept, p[:n]...)
	}

	if errors.Is(err, io.EOF) {
		b.eof = true
	}

	return n, err
}

// Close closes the body and ends the call to the target.
func (b *targetBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)

	return err
}

// Kept returns the body, once read to its end, when it fits in _keptBodyLimit.
func (b *targetBody) Kept() ([]byte, bool) {
	return b.kept, b.eof && !b.over
}

// forwardRequest returns the copy of r a proxy sends on, with ctx, without the hop-by-hop headers of the client's
// connection, nor its "Connection: close", and with id, the ID of the server, added to its VLatte-Via. A request whose
// VLatte-Via holds id already came back to the server: it fails. The copy shares the trailers of r, which the server
// sets on r once it reads the body to the end.
func forwardRequest(ctx context.Context, r *http.Request, id string) (*http.Request, error) {
	if cameBack(r.Header, id) {
		return nil, errProxyLoop
	}

	out := r.Clone(ctx)
	out.RequestURI = ""
	out.Close = false
	out.Trailer = r.Trailer
	removeRequestHopByHop(out.Header)
	out.Header.Add(_viaHeader, id)

	return out, nil
}

// removeHopByHop removes the hop-by-hop headers from h: the ones its Connection header names, and _forbiddenHeaders.
func removeHopByHop(h http.Header) {
	for _, v := range h.Values(HeaderConnection) {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}

	for _, name := range _forbiddenHeaders {
		h.Del(name)
	}
}

// removeRequestHopByHop removes the hop-by-hop headers from h, the header of a request that goes on. A Te that accepts
// trailers stays as "Te: trailers", which HTTP/2 and gRPC targets need.
func removeRequestHopByHop(h http.Header) {
	trailers := false
	for _, v := range h.Values("Te") {
		for token := range strings.SplitSeq(v, ",") {
			trailers = trailers || strings.EqualFold(strings.TrimSpace(token), "trailers")
		}
	}

	removeHopByHop(h)

	if trailers {
		h.Set("Te", "trailers")
	}
}

// isVlatteHeader reports whether name is the name of a header of vlatte's, such as VLatte-Request-Id. It fits
// maps.DeleteFunc.
func isVlatteHeader(name string, _ []string) bool {
	return strings.HasPrefix(http.CanonicalHeaderKey(name), "Vlatte-")
}

// displayURL returns u as errors and logs show it: without its password, as URL.Redacted does, and without the values
// of its query, which can hold credentials such as an API key: "?api_key=xxxxx". The names of the query stay, in order.
func displayURL(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Redacted()
	}

	c := u.Clone()

	params := strings.Split(c.RawQuery, "&")
	for i, param := range params {
		if name, _, ok := strings.Cut(param, "="); ok {
			params[i] = name + "=" + _maskedQueryValue
		}
	}

	c.RawQuery = strings.Join(params, "&")

	return c.Redacted()
}
