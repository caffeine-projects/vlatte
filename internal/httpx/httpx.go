// Package httpx holds the small HTTP helpers the mock servers and the webhook package share.
package httpx

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
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

// NewWebhookTransport returns the transport a server gives webhooks when its config has none. It is the server's:
// Close closes its idle connections. It has the limits of http.DefaultTransport, built here rather than copied, as a
// program may have replaced http.DefaultTransport.
func NewWebhookTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
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
