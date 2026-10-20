package mockhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// okTarget starts a target that answers "ok".
func okTarget(t *testing.T) *httptest.Server {
	t.Helper()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	t.Cleanup(target.Close)

	return target
}

func TestTransport_ProxyRepliesSendThroughTheCallersTransport(t *testing.T) {
	// The server's transport carries what its proxy replies send, such as through a client certificate or a proxy of
	// the caller's network. One upstream with other needs gets a transport of its own on its reply.
	target := okTarget(t)
	server, own := newCountingTransport(t), newCountingTransport(t)

	m := MustNew(Setup().Silent().Transport(server))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/server").Reply(From(target.URL)),
		Getf("/own").Reply(From(target.URL).Transport(own)),
	)

	status, body := get(t, m, "/server")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, int64(1), server.calls.Load())
	require.Zero(t, own.calls.Load())

	status, body = get(t, m, "/own")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, int64(1), own.calls.Load(), "the reply's transport goes first")
	require.Equal(t, int64(1), server.calls.Load())
}

func TestTransport_TheRecordingTargetGetsTheCallersTransport(t *testing.T) {
	// A recording sends its target the requests it takes through the server's transport, or its own.
	target := okTarget(t)

	tests := []struct {
		name    string
		options func(own *countingTransport) []RecordConfigurer
		want    func(server, own *countingTransport) *countingTransport
	}{
		{
			name:    "the server's",
			options: func(*countingTransport) []RecordConfigurer { return nil },
			want:    func(server, _ *countingTransport) *countingTransport { return server },
		},
		{
			name:    "its own first",
			options: func(own *countingTransport) []RecordConfigurer { return []RecordConfigurer{RecordTransport(own)} },
			want:    func(_, own *countingTransport) *countingTransport { return own },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, own := newCountingTransport(t), newCountingTransport(t)

			m := mockedServer(t, Setup().Silent().Transport(server))
			require.NoError(t, m.StartRecording(append([]RecordConfigurer{RecordDir(t.TempDir()), RecordTarget(target.URL)}, tt.options(own)...)...))

			_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
			require.Equal(t, "ok", string(body))
			require.Equal(t, int64(1), tt.want(server, own).calls.Load())
			require.Equal(t, int64(1), server.calls.Load()+own.calls.Load(), "one transport carries the call")
		})
	}
}

func TestTransport_TheForwardProxyGetsTheCallersTransport(t *testing.T) {
	// A request no mock matches goes to its host through the proxy's transport, or else the server's.
	target := okTarget(t)

	tests := []struct {
		name  string
		setup func(server, own *countingTransport) *ConfigBuilder
		want  func(server, own *countingTransport) *countingTransport
	}{
		{
			name:  "the server's",
			setup: func(server, _ *countingTransport) *ConfigBuilder { return Setup().Transport(server).Proxy() },
			want:  func(server, _ *countingTransport) *countingTransport { return server },
		},
		{
			name: "its own first",
			setup: func(server, own *countingTransport) *ConfigBuilder {
				return Setup().Transport(server).Proxy(&ProxyConfig{Transport: own})
			},
			want: func(_, own *countingTransport) *countingTransport { return own },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, own := newCountingTransport(t), newCountingTransport(t)

			m := MustNew(tt.setup(server, own).Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			proxyURL, err := url.Parse(m.URL())
			require.NoError(t, err)

			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			t.Cleanup(transport.CloseIdleConnections)

			res, err := (&http.Client{Transport: transport}).Get(target.URL + "/x")
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)

			require.Equal(t, int64(1), tt.want(server, own).calls.Load())
			require.Equal(t, int64(1), server.calls.Load()+own.calls.Load(), "one transport carries the call")
		})
	}
}

func TestTransport_ASettingTheCallersTransportWouldIgnoreFails(t *testing.T) {
	// #67: a transport of the caller's decides on its own whether it verifies its upstreams and which proxy it goes
	// through, so the settings that shape only the transports the server makes had no effect with it, silently: a mock
	// that asked to verify its target answered from a target nothing verified. They fail instead, naming the setting
	// and the transport. A setting left out, such as the defaults of a mock file, still works with any transport.
	target := okTarget(t)
	transport := newCountingTransport(t)

	t.Run("a proxy reply", func(t *testing.T) {
		tests := []struct {
			name  string
			setup *ConfigBuilder
			reply *ProxyReply
		}{
			{name: "SSLVerify with the server's", setup: Setup().Transport(transport), reply: From(target.URL).SSLVerify(true)},
			{name: "SkipSSLVerify with its own", setup: Setup(), reply: From(target.URL).Transport(transport).SkipSSLVerify()},
			{name: "SSLVerify(false) with its own", setup: Setup(), reply: From(target.URL).SSLVerify(false).Transport(transport)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				m := MustNew(tt.setup.Silent())
				t.Cleanup(m.Close)

				_, err := m.Mock(Getf("/p").Reply(tt.reply))
				require.ErrorContains(t, err, "reply_proxy: ssl_verify has no effect with a transport of the caller's")
			})
		}
	})

	t.Run("a proxy reply of a mock file", func(t *testing.T) {
		m := MustNew(Setup().Silent().Transport(transport))
		t.Cleanup(m.Close)

		_, err := m.Mock(FromBytes([]byte("request: {path: /p}\nproxy: {target: '"+target.URL+"', ssl_verify: true}\n"), "yaml"))
		require.ErrorContains(t, err, "ssl_verify has no effect with a transport of the caller's")

		_, err = m.Mock(FromBytes([]byte("request: {path: /p}\nproxy: {target: '"+target.URL+"'}\n"), "yaml"))
		require.NoError(t, err, "a mock that leaves ssl_verify out builds")
	})

	t.Run("the forward proxy", func(t *testing.T) {
		tests := []struct {
			name  string
			setup *ConfigBuilder
			want  string
		}{
			{
				name:  "Via with the server's",
				setup: Setup().Transport(transport).Proxy(&ProxyConfig{Via: "http://127.0.0.1:1"}),
				want:  "proxy: via has no effect with a transport of the caller's",
			},
			{
				name:  "SSLVerify with its own",
				setup: Setup().Proxy(&ProxyConfig{SSLVerify: true, Transport: transport}),
				want:  "proxy: ssl_verify has no effect with a transport of the caller's",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := New(tt.setup.Silent())
				require.ErrorContains(t, err, tt.want)
			})
		}
	})

	t.Run("the recording", func(t *testing.T) {
		m := mockedServer(t, Setup().Silent().Transport(transport))

		err := m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordSkipSSLVerify(true))
		require.ErrorContains(t, err, "recorder: skip_ssl_verify has no effect with a transport of the caller's")

		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)), "verifying is the default")
	})
}

func TestTransport_ADescribedProxyReplyLoadsBackWithTheCallersTransport(t *testing.T) {
	// A description that always held ssl_verify, the reply's default included, loaded back as a mock that set it, which
	// a server with a transport of the caller's refuses: it is described only when it was set.
	target := okTarget(t)

	m := MustNew(Setup().Silent().Transport(newCountingTransport(t)))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/p").Reply(From(target.URL)))
	proxy := scope.Mocks()[0].Describe().Props.(map[string]any)["proxy"].(map[string]any)
	require.NotContains(t, proxy, "ssl_verify")

	verified, err := AnyMethod().Reply(From(target.URL).SSLVerify(true)).Build(MustNew(Setup().Silent()))
	require.NoError(t, err)
	require.Equal(t, true, verified.Describe().Props.(map[string]any)["proxy"].(map[string]any)["ssl_verify"])
}

func TestForwardProxy_AViaThatIsNoProxyURLFailsNew(t *testing.T) {
	// A Via that didn't parse panicked in New, and one of a scheme no transport takes failed only when a request went
	// through it.
	for _, via := range []string{"http://[::1", "ftp://127.0.0.1:21", "proxy:3128"} {
		t.Run(via, func(t *testing.T) {
			_, err := New(Setup().Silent().Proxy(&ProxyConfig{Via: via}))
			require.ErrorContains(t, err, "expected an absolute http, https, socks5 or socks5h URL, such as http://proxy:3128")
		})
	}
}

func TestForwardProxy_PassesCompressedBodiesAsTheyCome(t *testing.T) {
	// The transport the forward proxy made asked for gzip on behalf of a client that didn't, and unzipped the answer:
	// the host saw an Accept-Encoding the client never sent, and the client got another body than the host's, without
	// its Content-Encoding. A body the host compressed goes on as it came.
	compressed := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	accepted := make(chan []string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted <- r.Header.Values("Accept-Encoding")

		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	// The client asks for no encoding, and doesn't unzip what it gets.
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)

	res, err := (&http.Client{Transport: transport}).Get(target.URL + "/x")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Empty(t, <-accepted, "the host got an Accept-Encoding the client didn't send")
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	require.Equal(t, compressed, body)
}

func TestForwardProxy_AViaWithoutAPortTakesItsSchemesDefault(t *testing.T) {
	// A Via that names no port is reached on its scheme's default one, as http.Transport does: a SOCKS5 Via on 1080.
	tests := []struct {
		via  string
		want string
	}{
		{via: "http://proxy.local", want: "proxy.local:80"},
		{via: "https://proxy.local", want: "proxy.local:443"},
		{via: "socks5://proxy.local", want: "proxy.local:1080"},
		{via: "socks5h://proxy.local", want: "proxy.local:1080"},
		{via: "http://proxy.local:3128", want: "proxy.local:3128"},
		{via: "http://[::1]", want: "[::1]:80"},
	}

	for _, tt := range tests {
		t.Run(tt.via, func(t *testing.T) {
			u, err := parseVia(tt.via)
			require.NoError(t, err)
			require.Equal(t, tt.want, viaAddr(u))
		})
	}
}
