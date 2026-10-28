package e2e

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// upstream starts a server on 127.0.0.1 that answers GET /hi with 200, for a mock server to reach.
func upstream(t *testing.T) *mockhttp.Server {
	t.Helper()

	up := mockhttp.MustNew(mockhttp.Setup().Silent().Addr("127.0.0.1:0"))
	up.MustStart()
	t.Cleanup(up.Close)
	up.MustMock(mockhttp.Getf("/hi").Reply(mockhttp.OK()))

	return up
}

func TestOutbound_ProxyReplies(t *testing.T) {
	// The outbound rules keep a server exposed to others, as in Docker, from reaching hosts it must not, such as the
	// cloud metadata address or an internal network, whoever adds the mock. They check the address the server
	// connects to, after DNS resolution, so a host name that resolves into a refused range is refused too.
	loopback := netip.MustParsePrefix("127.0.0.0/8")

	tests := []struct {
		name    string
		setup   *mockhttp.ConfigBuilder
		reached bool
	}{
		{
			name:    "no rules reach any address",
			setup:   mockhttp.Setup(),
			reached: true,
		},
		{
			name:    "a denied range is refused",
			setup:   mockhttp.Setup().OutboundDeny(loopback),
			reached: false,
		},
		{
			name:    "an allowed range is reached",
			setup:   mockhttp.Setup().OutboundAllow(loopback),
			reached: true,
		},
		{
			name:    "an address outside the allowed ranges is refused",
			setup:   mockhttp.Setup().OutboundAllow(netip.MustParsePrefix("10.0.0.0/8")),
			reached: false,
		},
		{
			name:    "deny wins over allow",
			setup:   mockhttp.Setup().OutboundAllow(loopback).OutboundDeny(netip.MustParsePrefix("127.0.0.1/32")),
			reached: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := upstream(t)

			m := mockhttp.MustNew(tc.setup.Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(mockhttp.Getf("/hi").Reply(mockhttp.From(up.URL())))

			res, err := http.Get(m.URL("/hi"))
			require.NoError(t, err)
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			if tc.reached {
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, int64(1), up.Hits())

				return
			}

			// The server's own answer to a reply that failed, which says why.
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
			require.Equal(t, vlatte.FailureReply, res.Header.Get(vlatte.HeaderFailure))
			require.Contains(t, string(body), vlatte.ErrOutboundRefused.Error())
			require.Zero(t, up.Hits(), "a refused address gets no request")
		})
	}
}

func TestOutbound_ForwardProxy(t *testing.T) {
	// A request no mock matches goes on to its host: the forward proxy follows the rules too.
	up := upstream(t)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Proxy().OutboundDeny(netip.MustParsePrefix("127.0.0.0/8")))
	m.MustStart()
	t.Cleanup(m.Close)

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(up.URL("/hi"))
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.Contains(t, string(body), vlatte.ErrOutboundRefused.Error())
	require.Zero(t, up.Hits())
}

func TestOutbound_ForwardProxyTunnels(t *testing.T) {
	// An https URL makes the client open a CONNECT tunnel, which the proxy dials itself, outside any transport: its
	// host follows the rules too.
	up := mockhttp.MustNew(mockhttp.Setup().Silent().Addr("127.0.0.1:0"))
	up.MustStartTLS()
	t.Cleanup(up.Close)
	up.MustMock(mockhttp.Getf("/hi").Reply(mockhttp.OK()))

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Proxy().OutboundDeny(netip.MustParsePrefix("127.0.0.0/8")))
	m.MustStart()
	t.Cleanup(m.Close)

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	t.Cleanup(client.CloseIdleConnections)

	// The proxy answers the CONNECT with its own error status, so the client's tunnel never opens.
	_, err = client.Get(up.URL("/hi"))
	require.ErrorContains(t, err, http.StatusText(mockhttp.StatusNoMatch))
	require.Zero(t, up.Hits())
}

func TestOutbound_Webhooks(t *testing.T) {
	// A webhook's target follows the rules: its URL comes from a mock, which anyone who reaches the admin API sets.
	up := upstream(t)

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().
		OutboundDeny(netip.MustParsePrefix("127.0.0.0/8")).
		OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{"url": up.URL("/hi")}}).
		Reply(mockhttp.NoContent()))

	// The response waits for the webhook, so the upstream is checked right after.
	res, err := http.Get(m.URL("/test"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Zero(t, up.Hits(), "a refused webhook sends nothing")
}
