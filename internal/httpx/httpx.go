// Package httpx holds the small HTTP helpers the mock servers and the webhook package share.
package httpx

import (
	"context"
	"net"
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
