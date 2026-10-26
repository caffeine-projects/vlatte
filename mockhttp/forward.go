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

	"golang.org/x/net/http/httpguts"
)

var (
	_ io.ReadCloser = (*targetBody)(nil)
	_ keptBody      = (*targetBody)(nil)
)

var (
	// errNoHeaders ends a call to an upstream that didn't send its response headers within the timeout.
	errNoHeaders = errors.New("proxy: no response headers in time")

	// errProxyLoop refuses a request that came back to the server that sent it on too often.
	errProxyLoop = errors.New("proxy loop: the request came back to this server again and again: its target, or a proxy on the way, is this server")
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

	// _viaHeader holds the IDs of the servers a request went through, once per pass. A server that gets back a request
	// it sent on _maxPasses times, such as through a proxy reply whose target is the server itself, refuses it rather
	// than send it on forever. It isn't Via, which upstreams read: an nginx with gzip_proxied off doesn't compress its
	// answer to a request with one.
	_viaHeader = "VLatte-Via"

	// _maxPasses is how often a request may go through a server: a route can pass a server more than once, such as
	// through a gateway that sends it back, and a loop passes it again and again.
	_maxPasses = 10

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

	if res.StatusCode == http.StatusSwitchingProtocols {
		// The connection is the proxy's now: the call's context no longer bounds it.
		cancel(nil)
		return switched(res, upgradeType(out.Header), display)
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

	// The server's trailers: the keys it announced now, and their values once its body is read to the end. The
	// transport adds the ones it didn't announce to the map, when there is one.
	if res.Trailer == nil {
		res.Trailer = make(http.Header)
	}

	stub.Trailer = res.Trailer

	return stub, nil
}

// switched returns res, the answer of a server that switched protocols, as asked for in asked, with the connection to
// the server, which the proxy pipes to the client's. A server that switched to another protocol, or a transport that
// gives no connection, fails it.
func switched(res *http.Response, asked, display string) (*Response, error) {
	conn, ok := res.Body.(io.ReadWriteCloser)
	if !ok {
		res.Body.Close()
		return nil, fmt.Errorf("the target %s switched protocols, and the transport gave no connection to it", display)
	}

	// A client may offer several protocols, and the target picks one.
	if got := upgradeType(res.Header); asked == "" || !httpguts.HeaderValuesContainsToken([]string{asked}, got) {
		conn.Close()
		return nil, fmt.Errorf("the target %s switched to the protocol %q, not %q", display, got, asked)
	}

	protocol := res.Header.Get(HeaderUpgrade)

	removeHopByHop(res.Header)
	maps.DeleteFunc(res.Header, isVlatteHeader)

	// The switch is about both connections, the target's and the client's: the client gets it too.
	res.Header.Set(HeaderConnection, "Upgrade")
	res.Header.Set(HeaderUpgrade, protocol)

	stub := newResponse()
	stub.StatusCode = res.StatusCode
	stub.Header = res.Header
	stub.upgrade = conn
	stub.proxied = true

	return stub, nil
}

// upgradeType returns the protocol h, the header of a request or a response, asks to switch to, or "" when it asks
// for none: its Connection holds "upgrade", and its Upgrade names the protocol.
func upgradeType(h http.Header) string {
	if !httpguts.HeaderValuesContainsToken(h[HeaderConnection], "upgrade") {
		return ""
	}

	return h.Get(HeaderUpgrade)
}

// close releases the idle connections of the transport the server made, if it did. The transport stays usable: a
// request that was being served still gets its answer.
func (u *upstream) close() {
	if u.owned != nil {
		u.owned.CloseIdleConnections()
	}
}

// targetBody is the body of a target's response, which streams to the client. With keep, it keeps what it reads, up
// to _keptBodyLimit, for what reads the body once it is sent, and closing it ends the call to the target.
type targetBody struct {
	io.ReadCloser

	cancel context.CancelCauseFunc
	cut    func() error // the cause of a cut of the call, such as by an upstream step, or nil
	keep   bool         // set before the body is read
	kept   []byte
	over   bool // read more than it keeps
	eof    bool
}

// Read reads the body, and keeps what it read, with keep. A read that fails once the call was cut fails with the
// cause of the cut, whatever error the cut ended the body with.
func (b *targetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	if err != nil && !errors.Is(err, io.EOF) && b.cut != nil {
		if cause := b.cut(); cause != nil {
			err = cause
		}
	}

	switch {
	case !b.keep || b.over:
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

// Kept returns the body, once read to its end, when it kept it, and it fits in _keptBodyLimit.
func (b *targetBody) Kept() ([]byte, bool) {
	return b.kept, b.keep && b.eof && !b.over
}

// keepBody has res keep its streamed body, if it is a target's, for what reads it once it is sent: callbacks, events
// or the log. A body nothing reads isn't kept, which spares a server that proxies many bodies their memory.
func keepBody(res *Response, keep bool) {
	if body, ok := res.BodyCloser.(*targetBody); ok {
		body.keep = keep
	}
}

// forwardRequest returns the copy of r a proxy sends on, with ctx, without the hop-by-hop headers of the client's
// connection, nor its "Connection: close", and with id, the ID of the server, added to its VLatte-Via. A request that
// asks to switch protocols, such as to a WebSocket, keeps asking, but for h2c, which asks for HTTP/2 on the client's
// connection to the proxy, which the proxy doesn't switch. A request whose VLatte-Via holds id _maxPasses times is in
// a loop: it fails. The copy shares the trailers of r, which the server sets on r once it reads the body to the end.
func forwardRequest(ctx context.Context, r *http.Request, id string) (*http.Request, error) {
	if looped(r.Header, id) {
		return nil, errProxyLoop
	}

	out := r.Clone(ctx)
	out.RequestURI = ""
	out.Close = false
	out.Trailer = r.Trailer

	var protocols []string
	for protocol := range strings.SplitSeq(upgradeType(out.Header), ",") {
		if protocol = strings.TrimSpace(protocol); protocol != "" && !strings.EqualFold(protocol, "h2c") {
			protocols = append(protocols, protocol)
		}
	}

	removeRequestHopByHop(out.Header)

	if len(protocols) > 0 {
		out.Header.Set(HeaderConnection, "Upgrade")
		out.Header.Set(HeaderUpgrade, strings.Join(protocols, ", "))
	}

	out.Header.Add(_viaHeader, id)

	return out, nil
}

// looped reports whether h, the header of a request, holds id _maxPasses times in its VLatte-Via: the request went
// through the server with that ID that often, and is in a loop.
func looped(h http.Header, id string) bool {
	passes := 0

	for _, v := range h.Values(_viaHeader) {
		for seen := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(seen) == id {
				passes++
			}
		}
	}

	return passes >= _maxPasses
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
