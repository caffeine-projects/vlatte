package mockhttp

import (
	"compress/gzip"
	"context"
	"errors"
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

// readStreamed reads the body of a proxied response, which streams, into Body: the trailers of the target are there
// once it is read.
func readStreamed(t *testing.T, res *Response) []byte {
	t.Helper()
	require.NoError(t, res.readBody())

	return res.Body
}

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
		assert.Equal(t, "hello world", string(readStreamed(t, res)))
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
		assert.Equal(t, expected, string(readStreamed(t, res)))
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
		readStreamed(t, res)

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
		assert.Equal(t, "", string(readStreamed(t, res)))
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

		require.NoError(t, err)
		readStreamed(t, res)
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

		require.NoError(t, err)
		readStreamed(t, res)
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
				require.Equal(t, tt.want, string(readStreamed(t, res)))
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

func TestProxyReply_StreamsTheTargetBody(t *testing.T) {
	// The proxy read the whole body of the target before replying: the client waited for all of it, and the server
	// held all of it. The client now reads each part as the target sends it.
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first;"))
		_ = http.NewResponseController(w).Flush()

		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}

		_, _ = w.Write([]byte("second"))
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(target.URL)))

	// The response, headers included, and its first part must arrive while the target holds the rest.
	type got struct {
		res   *http.Response
		first string
		err   error
	}

	arrived := make(chan got, 1)
	go func() {
		res, err := http.Get(m.URL() + "/p")
		if err != nil {
			arrived <- got{err: err}
			return
		}

		first := make([]byte, len("first;"))
		_, err = io.ReadFull(res.Body, first)
		arrived <- got{res: res, first: string(first), err: err}
	}()

	var g got
	select {
	case g = <-arrived:
	case <-time.After(2 * time.Second):
		close(release)

		if g = <-arrived; g.res != nil {
			g.res.Body.Close()
		}

		t.Fatal("the client got nothing while the target held the rest of its body")
	}

	require.NoError(t, g.err)
	defer g.res.Body.Close()
	require.Equal(t, "first;", g.first)

	close(release)

	rest, err := io.ReadAll(g.res.Body)
	require.NoError(t, err)
	require.Equal(t, "second", string(rest))
}

func TestProxyReply_TimeoutCoversTheHeadersOnly(t *testing.T) {
	// The timeout ended the whole call, so a body that took longer than it was cut. It covers the wait for the
	// response headers: a large or slow body takes the time it takes.
	tests := []struct {
		name       string
		timeout    time.Duration
		headerWait time.Duration
		bodyWait   time.Duration
		want       int
		wantBody   string
	}{
		{
			name:       "a body slower than the timeout",
			timeout:    50 * time.Millisecond,
			headerWait: 0,
			bodyWait:   200 * time.Millisecond,
			want:       http.StatusOK,
			wantBody:   "head;tail",
		},
		{
			name:       "headers slower than the timeout",
			timeout:    50 * time.Millisecond,
			headerWait: 500 * time.Millisecond,
			bodyWait:   0,
			want:       http.StatusTeapot,
			wantBody:   "no response headers within 50ms",
		},
		{
			name:       "0 waits forever",
			timeout:    0,
			headerWait: 100 * time.Millisecond,
			bodyWait:   0,
			want:       http.StatusOK,
			wantBody:   "head;tail",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(tc.headerWait):
				case <-r.Context().Done():
					return
				}

				_, _ = w.Write([]byte("head;"))
				_ = http.NewResponseController(w).Flush()
				time.Sleep(tc.bodyWait)
				_, _ = w.Write([]byte("tail"))
			}))
			t.Cleanup(target.Close)

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(Getf("/p").Reply(From(target.URL).Timeout(tc.timeout)))

			status, body := get(t, m, "/p")
			require.Equal(t, tc.want, status)
			require.Contains(t, body, tc.wantBody)
		})
	}
}

func TestProxyReply_MappersSeeTheWholeBody(t *testing.T) {
	// A proxied body streams, so Body is empty until it is read: mappers get it read whole. The target's
	// Content-Length goes with it, since a mapper can change the length.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("from the target"))
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/p").
		Map(func(_ *RequestValues, res *Response) error {
			res.Body = append([]byte("mapped: "), res.Body...)
			return nil
		}).
		Reply(From(target.URL)))

	status, body := get(t, m, "/p")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "mapped: from the target", body)
}

func TestProxyReply_SendsTheTargetTrailers(t *testing.T) {
	// The trailers of a streamed body come after it: those the target announced, and those it didn't.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Announced")
		_, _ = w.Write([]byte("body"))
		w.Header().Set("X-Announced", "a")
		w.Header().Set(http.TrailerPrefix+"X-Late", "l")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(target.URL)))

	res, err := http.Get(m.URL() + "/p")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, "body", string(body))
	require.Equal(t, "a", res.Trailer.Get("X-Announced"))
	require.Equal(t, "l", res.Trailer.Get("X-Late"))
}

// closeSignal is a body that reports its close.
type closeSignal struct {
	io.Reader

	closed chan struct{}
}

func (c closeSignal) Close() error {
	close(c.closed)
	return nil
}

func TestHandler_ClosesTheBodyOfAResponseItDoesntRender(t *testing.T) {
	// A mapper that fails leaves the response unrendered: a body a mapper set, such as a stream or a file, must still
	// be closed, or it keeps its connection or file open.
	closed := make(chan struct{})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").
		Map(func(_ *RequestValues, res *Response) error {
			res.BodyCloser = closeSignal{Reader: strings.NewReader("x"), closed: closed}
			return nil
		}).
		Map(func(*RequestValues, *Response) error { return errors.New("bad mapper") }).
		Reply(OK()))

	status, _ := get(t, m, "/x")
	require.Equal(t, m.Config().ErrorStatusCode, status)

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the body of the unrendered response was left open")
	}
}

func TestResponse_RenderClosesTheBodyCloserItDoesntSend(t *testing.T) {
	// A mapper can set Body on a response that came with a BodyCloser: Body is sent, and the BodyCloser still closed.
	closed := make(chan struct{})
	res := &Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       []byte("sent"),
		BodyCloser: closeSignal{Reader: strings.NewReader("not sent"), closed: closed},
	}

	rec := httptest.NewRecorder()
	require.NoError(t, res.Render(t.Context(), rec))
	require.Equal(t, "sent", rec.Body.String())

	select {
	case <-closed:
	default:
		t.Fatal("the BodyCloser was left open")
	}
}

func TestProxyReply_CallbacksSeeTheProxiedBody(t *testing.T) {
	// A proxied body streams to the client, so Body was empty for the request log, callbacks and events. It is kept
	// while it streams, up to a limit, and is Body again once sent.
	small := strings.Repeat("s", 1024)
	large := strings.Repeat("l", _keptBodyLimit+1)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte(large))
			return
		}

		_, _ = w.Write([]byte(small))
	}))
	t.Cleanup(target.Close)

	seen := make(chan string, 2)
	callback := func(_ context.Context, in vlatte.CallbackInput) error {
		seen <- string(in.(*CallbackInput).Response().Body)
		return nil
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(
		Getf("/small").Callback(callback).Reply(From(target.URL)),
		Getf("/large").Callback(callback).Reply(From(target.URL)),
	)

	_, body := get(t, m, "/small")
	require.Equal(t, small, body)
	require.Equal(t, small, <-seen)

	_, body = get(t, m, "/large")
	require.Len(t, body, len(large), "the client gets the whole body")
	require.Empty(t, <-seen, "a body over the limit isn't kept")
}

func TestProxyReply_NegativeTimeoutFailsTheBuild(t *testing.T) {
	// 0 waits forever: a negative timeout used to fail every request at once, and would now wait forever too.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	_, err := m.Mock(Getf("/p").Reply(From("http://localhost").Timeout(-time.Second)))
	require.ErrorContains(t, err, "reply_proxy: timeout must be 0 or more, not -1s")
}

func TestProxyReply_AMapperOfABodyThatBreaksOffFails(t *testing.T) {
	// A mapper reads the whole body of the target before anything is sent: a body that breaks off fails the reply,
	// and the client gets the failure's answer.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "100")
		_, _ = io.WriteString(w, "first part;")
		http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/p").Map(func(*RequestValues, *Response) error { return nil }).Reply(From(target.URL)))

	res, body := do(t, http.MethodGet, m.URL("/p"), nil, nil)
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "http: reading the response body")
	require.False(t, scoped.Called())
}

func TestProxyReply_ABodyThatBreaksOffCutsTheConnection(t *testing.T) {
	// The target's body streams: once the status is sent, a target that breaks off can't turn it into a failure.
	// The client's connection is cut, so it doesn't take part of the body for all of it, and the mock didn't serve.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "first part;")
		http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/p").Reply(From(target.URL)))

	res, err := http.Get(m.URL("/p"))
	require.NoError(t, err)

	_, err = io.ReadAll(res.Body)
	require.Error(t, err)
	require.NoError(t, res.Body.Close())
	require.False(t, scoped.Called())
}
