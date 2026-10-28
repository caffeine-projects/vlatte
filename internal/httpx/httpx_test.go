package httpx

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestWait_EndsWithTheContext(t *testing.T) {
	// A delay ends when the client goes away, or the server closes, and doesn't sleep for nothing.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	require.ErrorIs(t, Wait(ctx, time.Hour), context.Canceled)
	require.Less(t, time.Since(start), time.Minute)

	require.NoError(t, Wait(t.Context(), time.Millisecond))
}

func TestIsToken(t *testing.T) {
	// Methods and header names are tokens: a space, a separator or a control character would let them break the
	// request line or the header they go in.
	for _, s := range []string{"GET", "post", "X-Custom_Header", "M-SEARCH", "!#$%&'*+-.^_`|~"} {
		require.True(t, IsToken(s), s)
	}

	for _, s := range []string{"", "GET /", "X:Y", "a\r\nb", "é", "(x)", "a\x7f"} {
		require.False(t, IsToken(s), s)
	}
}

func TestListenAddr_TakesWhatAServerListensOn(t *testing.T) {
	// A port alone listens on every interface, as a server in a container needs; a host alone has no port to listen on.
	for _, addr := range []string{"", "8081", ":8081", "127.0.0.1:8081", "[::1]:8081"} {
		require.True(t, ListenAddr(addr), addr)
	}

	for _, addr := range []string{"localhost", "127.0.0.1", "http://localhost:8081"} {
		require.False(t, ListenAddr(addr), addr)
	}
}

func TestNewDialer_FollowsTheOutboundRules(t *testing.T) {
	// The rules check the address the dialer connects to. 0.0.0.0 connects to this host, as loopback does, so it
	// can't slip past rules that keep the server from itself.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })

	port := lis.Addr().(*net.TCPAddr).Port
	plain := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port)).String()
	unspecified := netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)).String()
	loopback := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	other := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	tests := []struct {
		name    string
		allow   []netip.Prefix
		deny    []netip.Prefix
		addr    string
		refused bool
	}{
		{name: "no rules", addr: plain},
		{name: "a denied address", deny: loopback, addr: plain, refused: true},
		{name: "the unspecified address, which reaches this host", deny: loopback, addr: unspecified, refused: true},
		{name: "an allowed address", allow: loopback, addr: plain},
		{name: "an address outside the allowed ranges", allow: other, addr: plain, refused: true},
		{name: "an address both allowed and denied", allow: loopback, deny: loopback, addr: plain, refused: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := NewDialer(tc.allow, tc.deny).DialContext(t.Context(), "tcp", tc.addr)
			if tc.refused {
				require.ErrorIs(t, err, vlatte.ErrOutboundRefused)
				return
			}

			require.NoError(t, err)
			require.NoError(t, conn.Close())
		})
	}
}

func TestParsePrefixes_TakesCIDRRangesOnly(t *testing.T) {
	// A bare address isn't a range: taking it as /32 would hide a typo, such as a missing mask.
	got, err := ParsePrefixes([]string{"10.0.0.0/8", "fd00::/8"})
	require.NoError(t, err)
	require.Equal(t, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}, got)

	_, err = ParsePrefixes([]string{"10.0.0.1"})
	require.EqualError(t, err, `expected address ranges in CIDR form, such as 10.0.0.0/8, not "10.0.0.1"`)
}
