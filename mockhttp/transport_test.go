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
