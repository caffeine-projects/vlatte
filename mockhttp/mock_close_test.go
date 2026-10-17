package mockhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// countingTarget starts a target that counts the connections it has open.
func countingTarget(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var open atomic.Int64

	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	target.Start()
	t.Cleanup(target.Close)

	return target, &open
}

// requireNoneOpen waits for the target to see every connection closed.
func requireNoneOpen(t *testing.T, open *atomic.Int64) {
	t.Helper()

	require.Eventually(t, func() bool { return open.Load() == 0 }, 2*time.Second, 10*time.Millisecond,
		"connections to the target left open: %d", open.Load())
}

// closingReplier answers 204 and counts its closes, as a replier holding a connection would.
type closingReplier struct {
	closes *atomic.Int64
}

func (r closingReplier) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusNoContent, Header: make(http.Header)}, nil
}

func (r closingReplier) Close() error {
	r.closes.Add(1)
	return nil
}

// closingReply makes a closingReplier for each mock, so each mock owns it.
type closingReply struct {
	closes *atomic.Int64
}

func (f closingReply) NewReplier(*Server) (Replier, error) {
	return closingReplier(f), nil
}

// brokenReply makes a replier that fails to close, as one whose connections are gone already.
type brokenReply struct {
	err error
}

func (f brokenReply) NewReplier(*Server) (Replier, error) {
	return brokenReplier(f), nil
}

type brokenReplier struct {
	err error
}

func (brokenReplier) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusNoContent, Header: make(http.Header)}, nil
}

func (r brokenReplier) Close() error {
	return r.err
}

// failingReply makes no replier.
type failingReply struct{}

func (failingReply) NewReplier(*Server) (Replier, error) {
	return nil, errors.New("no replier")
}

// countingTransport is a transport the caller shares, which a proxy must leave open.
type countingTransport struct {
	http.RoundTripper

	closes atomic.Int64
}

func (c *countingTransport) CloseIdleConnections() {
	c.closes.Add(1)
}

func TestProxyReply_ReleasesItsConnectionsWhenItLeaves(t *testing.T) {
	// #26: each build of a proxy mock makes a client of its own, which kept its idle connections to the target until
	// the server closed. A server that reloads mock files gained one connection on every reload.
	leave := []struct {
		name  string
		leave func(t *testing.T, m *Server, scope *Scope)
	}{
		{
			name: "remock",
			leave: func(t *testing.T, m *Server, scope *Scope) {
				_, err := m.Remock(t.Context(), scope, Getf("/other").Reply(OK()))
				require.NoError(t, err)
			},
		},
		{
			name: "unmock",
			leave: func(t *testing.T, m *Server, scope *Scope) {
				require.NoError(t, m.Unmock(t.Context(), scope))
			},
		},
		{
			name: "clean",
			leave: func(t *testing.T, m *Server, _ *Scope) {
				require.NoError(t, m.Clean(t.Context()))
			},
		},
		{
			name: "server close",
			leave: func(_ *testing.T, m *Server, _ *Scope) {
				m.Close()
			},
		},
	}

	for _, tc := range leave {
		t.Run(tc.name, func(t *testing.T) {
			target, open := countingTarget(t)

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			scope := m.MustMock(Getf("/p").Reply(From(target.URL)))

			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, int64(1), open.Load(), "the proxy keeps its connection to the target")

			tc.leave(t, m, scope)

			requireNoneOpen(t, open)
		})
	}

	t.Run("20 remocks", func(t *testing.T) {
		// The case of the issue: 20 requests, each followed by a Remock of the same mock, left 20 connections open.
		target, open := countingTarget(t)

		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		scope := m.MustMock(Getf("/p").Reply(From(target.URL)))
		for range 20 {
			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)

			var err error
			scope, err = m.Remock(t.Context(), scope, Getf("/p").Reply(From(target.URL)))
			require.NoError(t, err)
		}

		requireNoneOpen(t, open)
	})
}

func TestProxyReply_LeavesTheFactoryTransportOpen(t *testing.T) {
	// A factory's client can carry a transport the caller shares, such as http.DefaultTransport: closing its idle
	// connections when a mock leaves would cut them for everyone else using it.
	target, _ := countingTarget(t)

	inner := &http.Transport{}
	t.Cleanup(inner.CloseIdleConnections)

	shared := &countingTransport{RoundTripper: inner}

	m := MustNew(Setup().Silent().HTTPClient(func() *http.Client { return &http.Client{Transport: shared} }))
	m.MustStart()

	scope := m.MustMock(Getf("/p").Reply(From(target.URL)))

	status, _ := get(t, m, "/p")
	require.Equal(t, http.StatusOK, status)

	require.NoError(t, m.Unmock(t.Context(), scope))
	m.Close()

	require.Zero(t, shared.closes.Load())
}

func TestShared_IsNotClosedWithItsMocks(t *testing.T) {
	// A shared replier serves every mock built with it: one of them leaving must not close it for the others.
	var closes atomic.Int64

	r := closingReplier{closes: &closes}

	m := MustNew(Setup().Silent())
	m.MustStart()

	a := m.MustMock(Getf("/a").Reply(Shared(r)))
	m.MustMock(Getf("/b").Reply(Shared(r)))

	require.NoError(t, m.Unmock(t.Context(), a))

	status, _ := get(t, m, "/b")
	require.Equal(t, http.StatusNoContent, status)

	m.Close()

	require.Zero(t, closes.Load(), "the caller owns a shared replier")
}

func TestCompositeReplies_CloseTheRepliesTheyOwn(t *testing.T) {
	// #26: sequences and random replies make a replier for each reply they hold, and so own it, but not a shared one.
	tests := []struct {
		name  string
		reply func(owned, shared ReplierFactory) ReplierFactory
		want  int64
	}{
		{
			name:  "sequence",
			reply: func(owned, shared ReplierFactory) ReplierFactory { return Seq(owned, shared).OnSequenceEnded(owned) },
			want:  2, // the first reply and after_ended
		},
		{
			name:  "random",
			reply: func(owned, shared ReplierFactory) ReplierFactory { return Rand(owned, shared) },
			want:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ownedCloses, sharedCloses atomic.Int64

			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			scope := m.MustMock(Getf("/x").Reply(tc.reply(closingReply{closes: &ownedCloses}, Shared(closingReplier{closes: &sharedCloses}))))
			require.NoError(t, m.Unmock(t.Context(), scope))

			require.Equal(t, tc.want, ownedCloses.Load())
			require.Zero(t, sharedCloses.Load())
		})
	}

	builds := []struct {
		name  string
		reply func(owned ReplierFactory) ReplierFactory
	}{
		{
			name:  "sequence with a failing reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned, failingReply{}) },
		},
		{
			name:  "sequence with a failing after_ended",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned).OnSequenceEnded(failingReply{}) },
		},
		{
			name:  "sequence with a nil reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Seq(owned, nil) },
		},
		{
			name:  "random with a failing reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Rand(owned, failingReply{}) },
		},
		{
			name:  "random with a nil reply",
			reply: func(owned ReplierFactory) ReplierFactory { return Rand(owned, nil) },
		},
	}

	for _, tc := range builds {
		t.Run(tc.name, func(t *testing.T) {
			// The replies built before the failing one are dropped with the build: nothing else would close them.
			var closes atomic.Int64

			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			_, err := m.Mock(Getf("/x").Reply(tc.reply(closingReply{closes: &closes})))
			require.Error(t, err)
			require.Equal(t, int64(1), closes.Load())
		})
	}
}

func TestServer_CloseLogsTheRepliesThatFailToClose(t *testing.T) {
	// A mock whose replies fail to close is done all the same: Close logs why, for each of them.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	m.MustMock(Getf("/x").ID("x").Reply(Seq(brokenReply{errors.New("first gone")}, brokenReply{errors.New("second gone")})))

	m.Close()
	require.Contains(t, logs.String(), "server: Close: closing mock x")
	require.Contains(t, logs.String(), "first gone")
	require.Contains(t, logs.String(), "second gone")
}

func TestMock_ClosesItsReplyOnce(t *testing.T) {
	// Server.Close releases what the stored mocks hold, and keeps them stored for assertions. A mock that leaves
	// later doesn't close its reply a second time.
	var closes atomic.Int64

	m := MustNew(Setup().Silent())
	m.MustStart()
	m.MustMock(Getf("/x").Reply(closingReply{closes: &closes}))

	m.Close()
	require.Equal(t, int64(1), closes.Load())
	require.Len(t, m.ListPending(), 1, "the mock is still there for assertions")

	require.NoError(t, m.Clean(t.Context()))
	require.Equal(t, int64(1), closes.Load(), "closed once")
}

func TestServer_AClosedServerDoesntStartAgain(t *testing.T) {
	// Close released what the mocks hold for good: a closed server doesn't start again, even one that never started.
	for name, start := range map[string]func(*Server) error{
		"Start":    (*Server).Start,
		"StartTLS": (*Server).StartTLS,
	} {
		t.Run(name+" after a start", func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			m.Close()

			require.ErrorIs(t, start(m), http.ErrServerClosed)
			require.Empty(t, m.URL(), "nothing listens")
		})

		t.Run(name+" before any start", func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.Close()

			require.ErrorIs(t, start(m), http.ErrServerClosed)
			require.Empty(t, m.URL(), "nothing listens")
		})
	}
}

func TestServer_CloseReleasesTheForwardProxyConnections(t *testing.T) {
	// #26: the transport of the forward proxy was never closed: its connections to the targets outlived the server.
	target, open := countingTarget(t)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL + "/x")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	m.Close()

	requireNoneOpen(t, open)
}

func TestServer_CloseLeavesTheCallersProxyTransportOpen(t *testing.T) {
	// A ProxyConfig.Transport is the caller's, as a factory's client is.
	target, _ := countingTarget(t)

	inner := &http.Transport{}
	t.Cleanup(inner.CloseIdleConnections)

	shared := &countingTransport{RoundTripper: inner}

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Transport: shared, Timeout: time.Second}))
	m.MustStart()

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL + "/x")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	m.Close()

	require.Zero(t, shared.closes.Load())
}
