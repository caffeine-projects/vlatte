// Package httpx holds the small HTTP helpers the mock servers and the webhook package share.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caffeine-projects/vlatte"
)

// DefaultWebhookTimeout is how long a webhook waits for its answer when nothing sets its timeout.
const DefaultWebhookTimeout = 10 * time.Second

// Wait waits for d, or until ctx ends, and then returns ctx's error.
func Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NewDialer returns a dialer of the connections a server opens, with the limits of http.DefaultTransport's, which
// refuses an address in deny, or outside allow when allow isn't empty, and the unspecified address with either set,
// with [vlatte.ErrOutboundRefused]. It checks the address it connects to, once resolved.
func NewDialer(allow, deny []netip.Prefix) *net.Dialer {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if len(allow) > 0 || len(deny) > 0 {
		d.Control = func(_, address string, _ syscall.RawConn) error { return checkOutbound(address, allow, deny) }
	}

	return d
}

// checkOutbound returns vlatte.ErrOutboundRefused when address, the ip:port a dialer resolved, is in deny, or outside
// allow when allow isn't empty. It refuses the unspecified address, 0.0.0.0 or ::, which connects to this host
// whatever range holds it.
func checkOutbound(address string, allow, deny []netip.Prefix) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("server: outbound address %s: %w", address, err)
	}

	// An IPv4 address can come mapped into IPv6, as ::ffff:127.0.0.1, and a link-local one with a zone: neither is in
	// the ranges of its plain form otherwise.
	ip := ap.Addr().Unmap().WithZone("")
	in := func(p netip.Prefix) bool { return p.Contains(ip) }

	if ip.IsUnspecified() || slices.ContainsFunc(deny, in) || (len(allow) > 0 && !slices.ContainsFunc(allow, in)) {
		return vlatte.ErrOutboundRefused
	}

	return nil
}

// ParsePrefixes parses the address ranges of an outbound setting, each in CIDR form, for NewDialer.
func ParsePrefixes(list []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("expected address ranges in CIDR form, such as 10.0.0.0/8, not %q", s)
		}

		prefixes = append(prefixes, p)
	}

	return prefixes, nil
}

// NewWebhookTransport returns the transport a server gives webhooks when its config has none. It is the server's:
// Close closes its idle connections. It has the limits of http.DefaultTransport, built here rather than copied, as a
// program may have replaced http.DefaultTransport, and dials as NewDialer does with allow and deny.
func NewWebhookTransport(allow, deny []netip.Prefix) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           NewDialer(allow, deny).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// IsToken reports whether s is an HTTP token, as header names and methods are.
func IsToken(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range []byte(s) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`()<>@,;:\"/[]?={}`, c) >= 0 {
			return false
		}
	}

	return true
}

// ListenAddr reports whether addr is an address a server can listen on: empty, a port, or host:port.
func ListenAddr(addr string) bool {
	if addr == "" {
		return true
	}

	if _, err := strconv.Atoi(addr); err == nil {
		return true
	}

	_, _, err := net.SplitHostPort(addr)

	return err == nil
}
