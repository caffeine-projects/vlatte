package mockhttp

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

// envProxy starts a proxy for HTTP_PROXY and HTTPS_PROXY, which answers each request in absolute form itself, with
// "env proxy" and the URL, makes the tunnel of each CONNECT an echo, and sends what it took to seen.
func envProxy(t *testing.T, seen chan<- string) string {
	t.Helper()

	return "http://" + tcpHost(t, func(conn net.Conn) {
		br := bufio.NewReader(conn)

		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		if req.Method == http.MethodConnect {
			seen <- "CONNECT " + req.Host

			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
			_, _ = io.Copy(conn, br)

			return
		}

		seen <- req.URL.String()

		body := "env proxy " + req.URL.String()
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	})
}

// setProxyEnv sets the proxy of the environment, under both names of each variable: the lowercase one comes first.
func setProxyEnv(t *testing.T, httpProxy, httpsProxy, noProxy string) {
	t.Helper()

	for name, value := range map[string]string{"http_proxy": httpProxy, "https_proxy": httpsProxy, "no_proxy": noProxy} {
		t.Setenv(name, value)
		t.Setenv(strings.ToUpper(name), value)
	}
}

func TestTransport_GoesThroughTheProxyOfTheEnvironment(t *testing.T) {
	// The transports the server makes go through HTTP_PROXY and HTTPS_PROXY, as Docker sets them in its containers,
	// unless NO_PROXY leaves the host out, or the forward proxy has a Via: proxy replies, the requests the forward proxy
	// sends on, and its tunnels. The environment is read when the server is made, not once in a process, so a test can
	// set it. The host is an address of TEST-NET-1, which nothing answers, and no proxy leaves out as local.
	const host = "192.0.2.1"

	seen := make(chan string, 10)
	proxy := envProxy(t, seen)

	// The test's own requests don't go through the environment's proxy, which http.DefaultTransport would keep.
	direct := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(direct.CloseIdleConnections)

	read := func(t *testing.T) func(*http.Response, error) (int, string) {
		return func(res *http.Response, err error) (int, string) {
			t.Helper()
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			return res.StatusCode, string(body)
		}
	}

	t.Run("HTTP_PROXY and HTTPS_PROXY", func(t *testing.T) {
		setProxyEnv(t, proxy, proxy, "")

		m := MustNew(Setup().Silent().Proxy())
		m.MustStart()
		t.Cleanup(m.Close)
		m.MustMock(Getf("/p").Reply(From("http://" + host)))

		status, body := read(t)(direct.Get(m.URL("/p")))
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, "env proxy http://"+host+"/p", body)
		require.Equal(t, "http://"+host+"/p", <-seen)

		status, body = read(t)(proxyClient(t, m).Get("http://" + host + "/x"))
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, "env proxy http://"+host+"/x", body)
		require.Equal(t, "http://"+host+"/x", <-seen)

		conn, br := openTunnel(t, hostOf(t, m), host+":443", "")

		_, err := io.WriteString(conn, "tunnel\n")
		require.NoError(t, err)

		line, err := br.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "tunnel\n", line)
		require.Equal(t, "CONNECT "+host+":443", <-seen)
	})

	t.Run("NO_PROXY", func(t *testing.T) {
		setProxyEnv(t, proxy, proxy, host)

		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)
		m.MustMock(Getf("/p").Reply(From("http://" + host).Timeout(200 * time.Millisecond)))

		status, body := read(t)(direct.Get(m.URL("/p")))
		require.Equal(t, m.Config().ErrorStatusCode, status, body)

		select {
		case s := <-seen:
			t.Fatalf("the request went through the proxy: %s", s)
		default:
		}
	})

	t.Run("a Via", func(t *testing.T) {
		setProxyEnv(t, proxy, proxy, "")

		viaSeen := make(chan string, 1)

		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: envProxy(t, viaSeen)}))
		m.MustStart()
		t.Cleanup(m.Close)

		status, body := read(t)(proxyClient(t, m).Get("http://" + host + "/x"))
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, "http://"+host+"/x", <-viaSeen)

		select {
		case s := <-seen:
			t.Fatalf("the request went through the environment's proxy: %s", s)
		default:
		}
	})
}
