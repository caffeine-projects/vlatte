package mockhttp

import (
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestForward(t *testing.T) {
	t.Run("should forward and respond basic GET", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/path/test/example", r.URL.Path)
			assert.Equal(t, "all", r.URL.Query().Get("filter"))
			assert.Equal(t, "", r.Header.Get("x-to-be-removed"))
			assert.Equal(t, "ok", r.Header.Get("x-present"))
			assert.Equal(t, []string{"proxied", "ok"}, r.Header.Values("x-proxy"))

			w.Header().Add("Trailer", "x-test-trailer")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("hello world"))
			w.Header().Add("x-test-trailer", "trailer-ok")
		}))

		defer dest.Close()

		h := make(http.Header)
		h.Add("x-res", "ok")

		ph := make(http.Header)
		ph.Add("x-proxy", "ok")

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080/path/test/example?filter=all", nil)
		req.Header.Set("x-to-be-removed", "nok")
		req.Header.Set("x-present", "ok")
		rv := &RequestValues{RawRequest: req, URL: req.URL}

		reply := From(dest.URL).
			ForwardHeader("x-proxy", "proxied").
			ProxyHeaders(ph).
			RemoveProxyHeaders("x-to-be-removed").
			Header("x-res", "response").
			Headers(h)
		res, err := replierOf(t, reply).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, res.StatusCode)
		assert.Equal(t, "hello world", string(res.Body))
		assert.Equal(t, []string{"response", "ok"}, res.Header.Values("x-res"))
		assert.Equal(t, "trailer-ok", res.Trailer.Get("x-test-trailer"))
	})

	t.Run("should forward and respond POST with body", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)

			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)

			w.Write(b)
		}))

		defer dest.Close()

		expected := "test text"
		body := strings.NewReader(expected)
		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", body)
		rv := &RequestValues{RawRequest: req, URL: req.URL}

		u, _ := url.Parse(dest.URL)
		forward := From(u)
		res, err := replierOf(t, forward).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, expected, string(res.Body))
	})

	t.Run("should forward and respond POST with compressed body", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Add(HeaderContentEncoding, "gzip")
			w.Header().Add(HeaderContentType, "application/json")

			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)

			ww := gzip.NewWriter(w)

			defer ww.Close()

			_, _ = ww.Write(b)
		}))

		defer dest.Close()

		expected := "test text"
		body := strings.NewReader(expected)
		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", body)
		rv := &RequestValues{RawRequest: req, URL: req.URL}

		u, _ := url.Parse(dest.URL)
		forward := From(u)
		res, err := replierOf(t, forward).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})
		require.NoError(t, err)

		g, err := res.Gunzip()
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, expected, string(g))
	})

	t.Run("should forward and respond a No Content", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

		defer dest.Close()

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}

		forward := From(dest.URL)
		res, err := replierOf(t, forward).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, res.StatusCode)
		assert.Equal(t, "", string(res.Body))
	})

	t.Run("should remove prefix from URL", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/example", r.URL.Path)
			assert.Equal(t, "all", r.URL.Query().Get("filter"))

			w.WriteHeader(http.StatusOK)
		}))

		defer dest.Close()

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080/path/test/example?filter=all", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}
		reply := From(dest.URL).TrimPrefix("/path/test")
		res, err := replierOf(t, reply).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("should remove suffix from URL", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/path/test", r.URL.Path)
			assert.Equal(t, "all", r.URL.Query().Get("filter"))

			w.WriteHeader(http.StatusOK)
		}))

		defer dest.Close()

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080/path/test/example?filter=all", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}
		reply := From(dest.URL).TrimSuffix("/example")
		res, err := replierOf(t, reply).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("should keep escaped slashes when trimming the URL", func(t *testing.T) {
		// "%2F" is a "/" within a segment, not a separator: forwarded as "/", it names another path.
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(r.RequestURI))
		}))

		defer dest.Close()

		tests := []struct {
			name      string
			giveReply *ProxyReply
			givePath  string
			want      string
		}{
			{name: "prefix", giveReply: From(dest.URL).TrimPrefix("/api"), givePath: "/api/a%2Fb", want: "/a%2Fb"},
			{name: "suffix", giveReply: From(dest.URL).TrimSuffix("/x"), givePath: "/a%2Fb/x", want: "/a%2Fb"},
			// A trim matches the decoded path, however the client escaped it, and cuts the escaped path at the same place.
			{name: "prefix escaped", giveReply: From(dest.URL).TrimPrefix("/a b"), givePath: "/a%20b/c%2Fd", want: "/c%2Fd"},
			{name: "suffix escaped", giveReply: From(dest.URL).TrimSuffix("/x y"), givePath: "/a%2Fb/x%20y", want: "/a%2Fb"},
			{name: "prefix escaped needlessly", giveReply: From(dest.URL).TrimPrefix("/api"), givePath: "/%61pi/a%2Fb", want: "/a%2Fb"},
			{name: "prefix with a multibyte escape", giveReply: From(dest.URL).TrimPrefix("/café"), givePath: "/caf%C3%A9/a%2Fb", want: "/a%2Fb"},
			// What the prefix leaves starts with an escaped "/": sent as "%2Fv1", the target would not start with "/".
			{name: "prefix before an escaped slash", giveReply: From(dest.URL).TrimPrefix("/api"), givePath: "/api%2Fv1", want: "/v1"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				req, err := http.NewRequest(http.MethodGet, "http://localhost:8080"+tt.givePath, nil)
				require.NoError(t, err)

				rv := &RequestValues{RawRequest: req, URL: req.URL}
				res, err := replierOf(t, tt.giveReply).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

				require.NoError(t, err)
				require.Equal(t, tt.want, string(res.Body))
			})
		}
	})

	t.Run("should panic if the given raw target cannot be parsed to a URL", func(t *testing.T) {
		assert.Panics(t, func() {
			From(" https://fail test  ")
		})
	})

	t.Run("init From with string and *url.URL", func(t *testing.T) {
		addr := "https://localhost:8080"
		u, _ := url.Parse(addr)

		assert.Equal(t, From(u).target.String(), addr)
		assert.Equal(t, From(addr).target.String(), addr)
	})

	t.Run("should time out when the target is slower than the timeout", func(t *testing.T) {
		dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/path/test/example", r.URL.Path)

			// Wait until the reply gives up; the cap keeps a regression from hanging the test.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}

			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("hello world"))
		}))

		defer dest.Close()

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080/path/test/example", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}
		reply := From(dest.URL).
			Timeout(20 * time.Millisecond)
		res, err := replierOf(t, reply).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		require.Error(t, err)
		require.Nil(t, res)
	})

	t.Run("should follow redirects when using default configs", func(t *testing.T) {
		target := MustNew()
		target.MustStart()

		defer target.Close()

		m := MustNew()
		m.MustStart()

		defer m.Close()

		redirectScope := target.MustMock(Getf("/redirected").Reply(Accepted()))
		proxyScope := target.MustMock(Getf("/proxy").Reply(MovedPermanently(target.URL() + "/redirected")))
		scoped := m.MustMock(Getf("/proxy").Reply(From(target.URL())))

		res, err := http.Get(m.URL() + "/proxy")

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusAccepted, res.StatusCode)

		scoped.AssertCalled(t)
		proxyScope.AssertCalled(t)
		redirectScope.AssertCalled(t)
	})

	t.Run("passes a redirect of the target to a client that doesn't follow it", func(t *testing.T) {
		// A proxy sends with Transport.RoundTrip, which never follows redirects: the client gets the target's 301.
		target := MustNew()
		target.MustStart()

		defer target.Close()

		m := MustNew()
		m.MustStart()

		defer m.Close()

		redirectScope := target.MustMock(Getf("/redirected").Reply(Accepted()))
		proxyScope := target.MustMock(Getf("/proxy").Reply(MovedPermanently(target.URL() + "/redirected")))
		scoped := m.MustMock(Getf("/proxy").Reply(From(target.URL())))

		httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := httpClient.Get(m.URL() + "/proxy")

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusMovedPermanently, res.StatusCode)

		scoped.AssertCalled(t)
		proxyScope.AssertCalled(t)
		vlatte.NotCalled(t, redirectScope)
	})

	t.Run("tls", func(t *testing.T) {
		target := MustNew()
		t.Cleanup(func() { target.Close() })
		target.MustStartTLS()

		m := MustNew()
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		s1 := target.MustMock(Getf("/test").Reply(OK().PlainText("hi")))
		s2 := m.MustMock(Getf("/test").Reply(From(target.URL()).SSLVerify(true)))

		httpClient := &http.Client{}
		res, err := httpClient.Get(m.URL() + "/test")
		require.NoError(t, err)

		defer res.Body.Close()

		vlatte.NotCalled(t, s1)
		vlatte.NotCalled(t, s2)

		require.Equal(t, StatusNoMatch, res.StatusCode)

		s2.Clear(context.Background())
		s2 = m.MustMock(Getf("/test").Reply(From(target.URL()).SkipSSLVerify()))

		res, err = httpClient.Get(m.URL() + "/test")
		require.NoError(t, err)

		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)

		s1.AssertCalled(t)
		s2.AssertCalled(t)

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "hi", string(b))
	})
}

func TestProxyReply_ClosesTargetConnectionsOnClose(t *testing.T) {
	closed := make(chan struct{}, 1)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	target.Start()
	t.Cleanup(target.Close)

	m := MustNew()
	m.MustStart()
	m.MustMock(Getf("/p").Reply(From(target.URL)))

	res, err := http.Get(m.URL("/p"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	m.Close()

	// The proxy reply's connection to the target must not outlive the mock server.
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("connection to the proxy target was left open")
	}
}

func TestProxyReply_LeavesTheFactoryClientAsItWas(t *testing.T) {
	// Building a proxy mock set the Transport, with TLS verification off, on the client that the HTTPClient factory
	// returned, which can be a client it shares, such as http.DefaultClient. The mock gets a copy.
	shared := &http.Client{}
	m := MustNew(Setup().Silent().HTTPClient(func() *http.Client { return shared }))
	t.Cleanup(m.Close)

	_, err := AnyMethod().Reply(From("http://localhost")).Build(m)
	require.NoError(t, err)

	require.Nil(t, shared.Transport)
}

func TestProxyReply_FactoryWithoutClientFailsTheBuild(t *testing.T) {
	// The proxy copied the client the HTTPClient factory returned without checking it, so a factory that returned nil
	// made Mock panic, which only Must functions may do.
	m := MustNew(Setup().Silent().HTTPClient(func() *http.Client { return nil }))
	t.Cleanup(m.Close)

	_, err := AnyMethod().Reply(From("http://localhost")).Build(m)
	require.EqualError(t, err, "reply_proxy: the HTTP client factory returned no client")
}

func TestProxyReply_MockDescribesTheProxyAsBuilt(t *testing.T) {
	// A mock keeps the description of its reply from when it was built, so the description can't share the maps that
	// the setters of the reply change.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	rep := From("http://localhost").ForwardHeader("X-Fwd", "a")
	mock, err := AnyMethod().Reply(rep).Build(m)
	require.NoError(t, err)

	rep.ForwardHeader("X-Fwd", "b").Header("X-Res", "c").RemoveProxyHeaders("X-Drop")

	proxy := mock.Describe().Props.(map[string]any)["proxy"].(map[string]any)
	require.Equal(t, http.Header{"X-Fwd": {"a"}}, proxy["forward_header"])
	require.Empty(t, proxy["header"])
	require.Empty(t, proxy["remove_headers"])
}
