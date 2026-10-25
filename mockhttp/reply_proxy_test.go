package mockhttp

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
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
			RequestHeader("x-proxy", "proxied").
			RequestHeaders(ph).
			RemoveRequestHeaders("x-to-be-removed").
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
			// An escaped slash is inside a segment: "/api%2Fv1" is one segment, which "/api" doesn't trim (#69).
			{name: "prefix before an escaped slash", giveReply: From(dest.URL).TrimPrefix("/api"), givePath: "/api%2Fv1", want: "/api%2Fv1"},
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

func TestProxyReply_MockDescribesTheProxyAsBuilt(t *testing.T) {
	// A mock keeps the description of its reply from when it was built, so the description can't share the maps that
	// the setters of the reply change.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	rep := From("http://localhost").RequestHeader("X-Fwd", "a")
	mock, err := AnyMethod().Reply(rep).Build(m)
	require.NoError(t, err)

	rep.RequestHeader("X-Fwd", "b").Header("X-Res", "c").RemoveRequestHeaders("X-Drop")

	proxy := mock.Describe().Props.(map[string]any)["proxy"].(map[string]any)
	require.Equal(t, http.Header{"X-Fwd": {"a"}}, proxy["request_header"])
	require.Empty(t, proxy["header"])
	require.Empty(t, proxy["remove_request_headers"])
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

func TestProxyReply_LeavesTheClientRequestAsItCame(t *testing.T) {
	// #43: the proxy rewrote the client's request in place to send it to the target, so what ran after the reply,
	// such as mappers, callbacks and on_complete listeners, saw the target's URL and the forwarded headers instead of
	// what the client sent. Only the request sent to the target is the target's.
	type sight struct {
		who           string
		path          string
		host          string
		added         string
		authorization string
	}

	look := func(who string, r *http.Request) sight {
		return sight{
			who:           who,
			path:          r.URL.Path,
			host:          r.Host,
			added:         r.Header.Get("X-Added"),
			authorization: r.Header.Get(HeaderAuthorization),
		}
	}

	forwarded := make(chan sight, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		forwarded <- look("target", r)
	}))
	t.Cleanup(target.Close)

	seen := make(chan sight, 3)
	listener := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, e vlatte.OnComplete) error {
		seen <- look("listener", e.(*OnComplete).Request().RawRequest)
		return nil
	})

	m := MustNew(Setup().Silent().OnCompleteListener("look", listener))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/api/p").
		Map(func(rv *RequestValues, _ *Response) error {
			seen <- look("mapper", rv.RawRequest)
			return nil
		}).
		Callback(func(_ context.Context, in vlatte.CallbackInput) error {
			seen <- look("callback", in.(*CallbackInput).Request().RawRequest)
			return nil
		}).
		OnCompleteEvent(vlatte.EventConfig{Name: "look"}).
		Reply(From(target.URL).
			TrimPrefix("/api").
			RequestHeaders(http.Header{"X-Added": {"1"}}).
			RemoveRequestHeaders(HeaderAuthorization)))

	req, err := http.NewRequest(http.MethodGet, m.URL("/api/p"), nil)
	require.NoError(t, err)
	req.Header.Set(HeaderAuthorization, "Bearer client")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)

	require.Equal(t, sight{who: "target", path: "/p", host: targetURL.Host, added: "1"}, <-forwarded)

	for _, who := range []string{"mapper", "callback", "listener"} {
		require.Equal(t, sight{who: who, path: "/api/p", host: req.URL.Host, authorization: "Bearer client"}, <-seen)
	}
}

func TestProxyReply_LeavesHopByHopHeadersOut(t *testing.T) {
	// Hop-by-hop headers are about one connection: the client's to the mock server, or the target's to the proxy.
	// Passed on, the client's "Connection: close" closed the proxy's connection to the target after each call,
	// Proxy-Authorization gave the target credentials meant for a proxy, and an Upgrade or a Connection token makes
	// Go's HTTP/2 client refuse the request. A request's "Te: trailers" goes on: HTTP/2 and gRPC targets need it. Te is
	// a request header: a target's goes no further.
	type forwarded struct {
		header http.Header
		close  bool
	}

	got := make(chan forwarded, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- forwarded{header: r.Header.Clone(), close: r.Close}

		w.Header().Set(HeaderConnection, "X-Target-Hop")
		w.Header().Set("X-Target-Hop", "1")
		w.Header().Set("Te", "trailers")
		w.Header().Set("X-Target-End", "kept")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/p").Reply(From(target.URL)))

	req, err := http.NewRequest(http.MethodGet, m.URL("/p"), nil)
	require.NoError(t, err)
	req.Header.Set(HeaderConnection, "close, X-Client-Hop")
	req.Header.Set("X-Client-Hop", "1")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authorization", "Basic cHJveHk6c2VjcmV0")
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set(HeaderUpgrade, "websocket")
	req.Header.Set("Te", "trailers")
	req.Header.Set("X-Client-End", "kept")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	f := <-got
	require.False(t, f.close, "the client's Connection: close is for its own connection")

	for _, name := range []string{HeaderConnection, "X-Client-Hop", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection", HeaderUpgrade} {
		require.Empty(t, f.header.Values(name), "the target got %s", name)
	}

	require.Equal(t, "trailers", f.header.Get("Te"))
	require.Equal(t, "kept", f.header.Get("X-Client-End"))

	require.Empty(t, res.Header.Values("X-Target-Hop"), "the client got a header the target's Connection named")
	require.Empty(t, res.Header.Values("Te"), "the client got the target's Te")
	require.Equal(t, "kept", res.Header.Get("X-Target-End"))
}

func TestProxyReply_SendsThePathTheTargetExpects(t *testing.T) {
	// The target's path and query come first, as the base of an API, such as http://api/v1. The trims cut the request
	// path before it is joined. What is sent always starts with "/": "GET users" is no request a target accepts. An
	// escaped "/" names another path than "/": it stays escaped.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.RequestURI)
	}))
	t.Cleanup(target.Close)

	tests := []struct {
		name   string
		target string
		reply  func(*ProxyReply) *ProxyReply
		path   string
		want   string
	}{
		{name: "a prefix with its slash", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api/") }, path: "/api/users", want: "/users"},
		{name: "a prefix before escaped slashes", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api") }, path: "/api%2Fv1%2Fx", want: "/api%2Fv1%2Fx"},
		{name: "a prefix of a segment with escaped slashes", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api") }, path: "/api/v1%2Fx", want: "/v1%2Fx"},
		{name: "a path trimmed to nothing", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/svc") }, path: "/svc", want: "/"},
		// #69: the prefix trimmed text, so "/api" turned "/apiary" into "/ary". It trims whole segments.
		{name: "a prefix that ends inside a segment", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api") }, path: "/apiary", want: "/apiary"},
		{name: "a prefix of whole segments", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api/v1") }, path: "/api/v1/users", want: "/users"},
		{name: "a prefix that ends inside a later segment", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/api/v") }, path: "/api/v1/users", want: "/api/v1/users"},
		{name: "the target's path", target: "/api", path: "/users", want: "/api/users"},
		{name: "the target's path ending with a slash", target: "/api/", path: "/users", want: "/api/users"},
		{name: "the target's path and the root", target: "/api", path: "/", want: "/api/"},
		{name: "the target's path and a path trimmed to nothing", target: "/api", reply: func(r *ProxyReply) *ProxyReply { return r.TrimPrefix("/svc") }, path: "/svc", want: "/api"},
		{name: "the target's escaped path", target: "/a%2Fb", path: "/c", want: "/a%2Fb/c"},
		{name: "the target's path and a suffix", target: "/api", reply: func(r *ProxyReply) *ProxyReply { return r.TrimSuffix(".json") }, path: "/users.json", want: "/api/users"},
		{name: "the target's query", target: "/api?k=1", path: "/users", want: "/api/users?k=1"},
		{name: "both queries", target: "?k=1", path: "/users?q=2", want: "/users?k=1&q=2"},
		{name: "an empty query", target: "/api", path: "/users?", want: "/api/users?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reply := From(target.URL + tt.target)
			if tt.reply != nil {
				reply = tt.reply(reply)
			}

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(AnyMethod().Reply(reply))

			status, body := get(t, m, tt.path)
			require.Equal(t, http.StatusOK, status, body)
			require.Equal(t, tt.want, body)
		})
	}
}

func TestProxyReply_SendsTheTargetCredentials(t *testing.T) {
	// A transport sends no user info of a URL, so a target such as http://user:pass@api got no credentials, and
	// answered 401 without saying why. As http.Client does, they go as Basic auth, unless the request has its own
	// Authorization.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(HeaderAuthorization)
	}))
	t.Cleanup(target.Close)

	withCredentials, err := url.Parse(target.URL)
	require.NoError(t, err)
	withCredentials.User = url.UserPassword("user", "secret")

	basic := (&http.Request{Header: http.Header{}})
	basic.SetBasicAuth("user", "secret")

	tests := []struct {
		name          string
		reply         *ProxyReply
		authorization string
		want          string
	}{
		{name: "the target's", reply: From(withCredentials.String()), want: basic.Header.Get(HeaderAuthorization)},
		{name: "the client's first", reply: From(withCredentials.String()), authorization: "Bearer client", want: "Bearer client"},
		{name: "the target's, without the client's", reply: From(withCredentials.String()).RemoveRequestHeaders(HeaderAuthorization), authorization: "Bearer client", want: basic.Header.Get(HeaderAuthorization)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/p").Reply(tt.reply))

			req, err := http.NewRequest(http.MethodGet, m.URL("/p"), nil)
			require.NoError(t, err)

			if tt.authorization != "" {
				req.Header.Set(HeaderAuthorization, tt.authorization)
			}

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tt.want, <-got)
		})
	}
}

func TestProxyReply_KeepsTheTargetCredentialsOutOfErrorsAndLogs(t *testing.T) {
	// #65: the target's URL went whole into the error a failing call answers the client with and logs, with the
	// password of its user info, and the values of its query, such as the API key of APIs that take one there. Both
	// are hidden. The mock's description keeps the target as it was set, so it loads back as the same mock (#42): the
	// admin API that serves it is as trusted as the mock files.
	var logs bytes.Buffer
	logger := zerolog.New(&logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false))
	m.MustStart()
	t.Cleanup(m.Close)

	// Nothing listens on port 1: the call fails.
	const target = "http://user:secret@127.0.0.1:1/v1?api_key=SECRET-KEY&flag&page=2"
	scoped := m.MustMock(Getf("/p").Reply(From(target)))

	status, body := get(t, m, "/p")
	require.Equal(t, m.Config().ErrorStatusCode, status)
	require.Contains(t, body, "reply_proxy: calling the target http://user:xxxxx@127.0.0.1:1/v1?api_key=xxxxx&flag&page=xxxxx")

	for name, out := range map[string]string{"the answer": body, "the log": logs.String()} {
		require.NotContains(t, out, "secret", "%s holds the password", name)
		require.NotContains(t, out, "SECRET-KEY", "%s holds the API key", name)
	}

	proxy := scoped.Mocks()[0].Describe().Props.(map[string]any)["proxy"].(map[string]any)
	require.Equal(t, target, proxy["target"])
}

func TestProxyReply_AnswersWithTheProxysDebugHeadersOnly(t *testing.T) {
	// #60: a target that is a vlatte server sends its own VLatte-* debug headers, which went on to the client with the
	// proxy's: two values for each, and the target's alone with NoDebugHeaders, as if they were the proxy's.
	target := MustNew(Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)
	target.MustMock(Getf("/p").Reply(OK()))

	for _, noDebugHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("NoDebugHeaders=%t", noDebugHeaders), func(t *testing.T) {
			setup := Setup().Silent()
			if noDebugHeaders {
				setup.NoDebugHeaders()
			}

			m := MustNew(setup)
			m.MustStart()
			t.Cleanup(m.Close)
			scoped := m.MustMock(Getf("/p").Reply(From(target.URL())))

			status, header, _ := getFull(t, m, "/p")
			require.Equal(t, http.StatusOK, status)

			for _, name := range []string{vlatte.RequestIDHeader, vlatte.MatchedMockIDHeader, vlatte.ProxiedHeader} {
				if noDebugHeaders {
					require.Empty(t, header.Values(name), name)
				} else {
					require.Len(t, header.Values(name), 1, name)
				}
			}

			if !noDebugHeaders {
				require.Equal(t, scoped.Mocks()[0].ID(), header.Get(vlatte.MatchedMockIDHeader), "the proxy's mock")
			}
		})
	}
}

func TestProxyReply_ARequestThatComesBackFails(t *testing.T) {
	// A proxy reply whose target is its own server sent each request back to itself, which sent it on again, until the
	// server ran out of connections. The request that comes back fails as a loop: the client gets that failure.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/loop").Reply(From(m.URL())))

	status, body := get(t, m, "/loop")
	require.Equal(t, m.Config().ErrorStatusCode, status)
	require.Contains(t, body, "proxy loop: the request came back to this server")

	// A chain of servers isn't a loop: each sends the request on once.
	last := MustNew(Setup().Silent())
	last.MustStart()
	t.Cleanup(last.Close)
	last.MustMock(Getf("/chain").Reply(OK().PlainText("end")))

	middle := MustNew(Setup().Silent())
	middle.MustStart()
	t.Cleanup(middle.Close)
	middle.MustMock(Getf("/chain").Reply(From(last.URL())))

	m.MustMock(Getf("/chain").Reply(From(middle.URL())))

	status, body = get(t, m, "/chain")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "end", body)

	// Nor is a route that passes a server more than once, such as through a gateway that sends it back: m, middle, m
	// again, then last. A target's path comes before the request's.
	m.MustMock(Getf("/round").Reply(From(middle.URL())))
	middle.MustMock(Getf("/round").Reply(From(m.URL("/back"))))
	m.MustMock(Getf("/back/round").Reply(From(last.URL())))
	last.MustMock(Getf("/back/round").Reply(OK().PlainText("end")))

	status, body = get(t, m, "/round")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "end", body)
}

func TestProxyReply_AHopByHopRequestHeaderFailsTheBuild(t *testing.T) {
	// A forward header can't put back what the proxy leaves out of every request: a hop-by-hop header is about one
	// connection, and "Connection: Upgrade" or "Upgrade: websocket" asks for a protocol switch no proxy reply can pass
	// on. The build says so, rather than the proxy dropping the header without a word.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	tests := []struct {
		name  string
		reply *ProxyReply
		want  string
	}{
		{name: "Connection", reply: From("http://localhost").RequestHeader(HeaderConnection, "Upgrade"), want: "Connection"},
		{name: "Upgrade", reply: From("http://localhost").RequestHeaders(http.Header{HeaderUpgrade: {"websocket"}}), want: "Upgrade"},
		{name: "in any case", reply: From("http://localhost").RequestHeader("proxy-authorization", "Basic cHJveHk6c2VjcmV0"), want: "Proxy-Authorization"},
		{name: "Te other than trailers", reply: From("http://localhost").RequestHeader("Te", "gzip"), want: "Te"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AnyMethod().Reply(tt.reply).Build(m)
			require.EqualError(t, err, fmt.Sprintf("reply_proxy: request header %q is hop-by-hop: a proxy doesn't send it on", tt.want))
		})
	}

	t.Run("a mock file", func(t *testing.T) {
		_, err := m.Mock(FromBytes([]byte("request:\n  path: /p\nproxy:\n  target: http://localhost\n  request_header: {Connection: Upgrade}\n"), "yaml"))
		require.ErrorContains(t, err, `reply_proxy: request header "Connection" is hop-by-hop`)
	})

	t.Run("Te: trailers goes on", func(t *testing.T) {
		// The one hop-by-hop header a proxy sends on, as for a client's: HTTP/2 and gRPC targets need it.
		got := make(chan string, 1)
		target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got <- r.Header.Get("Te") }))
		t.Cleanup(target.Close)

		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		m.MustMock(Getf("/p").Reply(From(target.URL).RequestHeader("Te", "trailers")))

		status, _ := get(t, m, "/p")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "trailers", <-got)
	})
}

func TestProxyReply_ATargetThatIsNoHTTPURLFailsTheBuild(t *testing.T) {
	// A target without a scheme or a host, such as localhost:8080, which parses with "localhost" as its scheme, built
	// a mock that failed every request with "unsupported protocol scheme": the build says what is wrong instead.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	for _, target := range []string{"", "localhost:8080", "/api", "ftp://files.local"} {
		t.Run(target, func(t *testing.T) {
			_, err := AnyMethod().Reply(From(target)).Build(m)
			require.EqualError(t, err, fmt.Sprintf("reply_proxy: target %q: expected an absolute http or https URL, such as http://api.local:8080", target))
		})
	}

	t.Run("nil", func(t *testing.T) {
		require.PanicsWithError(t, "reply_proxy: target: nil URL", func() { From((*url.URL)(nil)) })
	})
}

func TestProxyReply_SendsTheClientTrailers(t *testing.T) {
	// A client's trailers come after its body, and the server sets them on the request once it reads the body to its
	// end. The request sent to the target is a copy: one made before the end, with a body the server read only part of,
	// had trailers without values.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		got <- r.Trailer.Get("X-Sum")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().MaxBodyParsingLimit(4))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/p").Reply(From(target.URL)))

	// A reader of unknown length goes chunked, with the trailers after it.
	req, err := http.NewRequest(http.MethodPost, m.URL("/p"), io.MultiReader(strings.NewReader("0123456789")))
	require.NoError(t, err)
	req.Trailer = http.Header{"X-Sum": {"abc"}}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "abc", <-got)
}

// sentTransport is a transport of the caller's that keeps the request it is given, and answers 204 without sending it.
type sentTransport struct {
	sent chan *http.Request
}

func (s sentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.sent <- r
	return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
}

func TestProxyReply_SendsTheTargetHostAsIs(t *testing.T) {
	// The request goes to the target's host as the URL names it, an IPv6 address in brackets included. The target's
	// user info goes as Basic auth, never in the host.
	rt := sentTransport{sent: make(chan *http.Request, 1)}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/users").Reply(From("http://user:secret@[::1]:8080/api").Transport(rt)))

	status, _ := get(t, m, "/users")
	require.Equal(t, http.StatusNoContent, status)

	sent := <-rt.sent
	require.Equal(t, "http://[::1]:8080/api/users", sent.URL.String())
	require.Equal(t, "[::1]:8080", sent.Host)

	user, password, ok := sent.BasicAuth()
	require.True(t, ok)
	require.Equal(t, "user", user)
	require.Equal(t, "secret", password)
}

func TestProxyReply_RefusesDotSegments(t *testing.T) {
	// The target's path comes first, so ".." would climb out of it: /../../admin after http://api/v1 is /admin to a
	// target that resolves dot segments. The proxy refuses them however they are written: plain, escaped, with an
	// escaped "/" that a target decodes before resolving, with parameters, or before a backslash. A target without a
	// path has nothing to leave: the path goes as the client sent it, the user's liberty.
	var calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(From(target.URL + "/api/v1")))

	// Some targets read more as a dot segment: one with parameters, "..;", or split on a backslash, as Windows does.
	for _, path := range []string{"/../../admin", "/%2e%2e/%2e%2e/admin", "/..%2F..%2Fadmin", "/./admin", "/..;/..;/admin", "/..%5C..%5Cadmin"} {
		t.Run(path, func(t *testing.T) {
			res, body := do(t, http.MethodGet, m.URL()+path, nil, nil)
			require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
			require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
			require.Contains(t, string(body), `has a "." or ".." segment`)
		})
	}

	require.Zero(t, calls.Load(), "the target got a path with a dot segment")

	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, r.RequestURI) }))
	t.Cleanup(echo.Close)

	// A target path of "/" is none: the join leaves it out too.
	for _, path := range []string{"", "/"} {
		t.Run("a target without a path: "+cmp.Or(path, "none"), func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(AnyMethod().Reply(From(echo.URL + path)))

			status, body := get(t, m, "/../admin")
			require.Equal(t, http.StatusOK, status, body)
			require.Equal(t, "/../admin", body)
		})
	}
}

func TestProxyReply_SendsTheTargetCredentialsOverTLS(t *testing.T) {
	// Whether a target with credentials gets its certificate verified is the user's call: by default it isn't, as for
	// any target, so one with a self-signed certificate gets them. A transport of the caller's decides for itself.
	got := make(chan string, 1)
	target := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(HeaderAuthorization)
	}))
	t.Cleanup(target.Close)

	withCredentials, err := url.Parse(target.URL)
	require.NoError(t, err)
	withCredentials.User = url.UserPassword("user", "secret")

	tests := []struct {
		name  string
		reply *ProxyReply
	}{
		{name: "by default", reply: From(withCredentials.String())},
		{name: "through a transport of the caller's", reply: From(withCredentials.String()).Transport(target.Client().Transport)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/p").Reply(tt.reply))

			status, _ := get(t, m, "/p")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, "Basic dXNlcjpzZWNyZXQ=", <-got, "user:secret")
		})
	}
}

func TestProxyReply_HeadersOfTheMockReplaceThoseOfTheSameName(t *testing.T) {
	// A header of the mock's was added to the client's, and to the target's: the target got the client's token and the
	// mock's, and the client two Content-Types. The mock's values replace those of the same name. Several values of the
	// mock's go together, and other headers stay as they came.
	got := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()

		w.Header().Set(HeaderContentType, "text/plain")
		w.Header().Set("X-Target", "kept")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(target.URL).
		RequestHeader("X-Token", "mock").
		RequestHeader("X-Tags", "a").
		RequestHeader("X-Tags", "b").
		Header(HeaderContentType, "application/json")))

	req, err := http.NewRequest(http.MethodGet, m.URL("/p"), nil)
	require.NoError(t, err)
	req.Header.Set("X-Token", "client")
	req.Header.Set("X-Client", "kept")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	sent := <-got
	require.Equal(t, []string{"mock"}, sent.Values("X-Token"))
	require.Equal(t, []string{"a", "b"}, sent.Values("X-Tags"))
	require.Equal(t, []string{"kept"}, sent.Values("X-Client"))

	require.Equal(t, []string{"application/json"}, res.Header.Values(HeaderContentType))
	require.Equal(t, []string{"kept"}, res.Header.Values("X-Target"))
}

func TestProxyReply_AStepThatChangesTheLengthKeepsTheAnswer(t *testing.T) {
	// #59: a proxied answer kept the target's Content-Length, which a step that changes the body made wrong: the server
	// failed the write, the proxy cut the connection, and the client got no answer at all.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Step(appendStep("<step>")).Reply(From(target.URL)))

	status, body := get(t, m, "/p")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "hello<step>", body)
}

// upstreamReceived is what a target got of a request.
type upstreamReceived struct {
	body             string
	contentLength    int64
	transferEncoding []string
}

// receivingTarget starts a target that sends what it got of each request on got.
func receivingTarget(t *testing.T, got chan<- upstreamReceived) *httptest.Server {
	t.Helper()

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- upstreamReceived{body: string(b), contentLength: r.ContentLength, transferEncoding: r.TransferEncoding}
	}))
	t.Cleanup(target.Close)

	return target
}

func TestProxyReply_UpstreamStepsChangeTheBodyTheTargetGets(t *testing.T) {
	// A step on what the mock sends on changes the request its target gets, its length included: the target gets it
	// chunked, so the length it reads is the body's.
	got := make(chan upstreamReceived, 1)
	target := receivingTarget(t, got)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Postf("/up").UpstreamStep(appendStep("!")).Reply(From(target.URL)))

	res, err := http.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, upstreamReceived{body: "ping!", contentLength: -1, transferEncoding: []string{"chunked"}}, <-got)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestProxyReply_ARequestWithoutABodyHasNothingForTheUpstreamSteps(t *testing.T) {
	// The upstream steps see the body only: a request without one goes to the target as it came.
	got := make(chan upstreamReceived, 1)
	target := receivingTarget(t, got)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/up").UpstreamStep(failingStep{}).Reply(From(target.URL)))

	status, _ := get(t, m, "/up")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, upstreamReceived{}, <-got)
}

func TestProxyReply_AFailingUpstreamStepFailsTheReply(t *testing.T) {
	// A step that fails what the mock sends on fails the call to the target: the reply failed, and the client gets
	// vlatte's answer.
	target := receivingTarget(t, make(chan upstreamReceived, 1))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Postf("/up").UpstreamStep(failingStep{}).Reply(From(target.URL)))

	res, err := http.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "step: failed")
	require.EqualValues(t, 0, scoped.Hits())
}

func TestProxyReply_AnUpstreamAbortCutsTheAnswer(t *testing.T) {
	// A step that cuts what the mock sends on cuts the client's answer too: the cut is the mock's answer, so it
	// counts.
	target := receivingTarget(t, make(chan upstreamReceived, 1))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Postf("/up").UpstreamStep(limitStep{n: 2, err: vlatte.ErrAbort}).Reply(From(target.URL)))

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	_, err := client.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.ErrorIs(t, err, io.EOF)
	require.EqualValues(t, 1, scoped.Hits())
}

// cutBody sends first, then waits for ctx to end, and fails as a transport whose connection was cut does: with an
// error of its own, not the cause of ctx.
type cutBody struct {
	ctx   context.Context
	first []byte
}

func (b *cutBody) Read(p []byte) (int, error) {
	if len(b.first) > 0 {
		n := copy(p, b.first)
		b.first = b.first[n:]

		return n, nil
	}

	<-b.ctx.Done()

	return 0, net.ErrClosed
}

func (b *cutBody) Close() error { return nil }

func TestProxyReply_AnUpstreamAbortCutsAnAnswerThatStarted(t *testing.T) {
	// A target can answer before it read the body, as a stream does: a step that then cuts what the mock sends on
	// cuts the client's answer where it got, not as one that looks whole. The cut is the mock's answer: it counts,
	// whatever error the cut ends the target's answer with.
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := &cutBody{ctx: r.Context(), first: []byte("start")}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, ContentLength: -1, Request: r}, nil
	})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	release := make(chan struct{})
	scoped := m.MustMock(Postf("/up").
		UpstreamStep(abortOn{release: release, err: errors.New("step: cut")}).
		Reply(From("http://target.test").Transport(transport)))

	res, err := http.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.NoError(t, err)

	first := make([]byte, len("start"))
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err)
	require.Equal(t, "start", string(first))

	close(release)

	rest, err := io.ReadAll(res.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Empty(t, rest)
	require.NoError(t, res.Body.Close())
	require.EqualValues(t, 1, scoped.Hits())
}

// closedBody is a body that tells closed once it is closed.
type closedBody struct {
	io.Reader

	closed chan<- struct{}
}

func (b closedBody) Close() error {
	close(b.closed)
	return nil
}

func TestProxyReply_AnUpstreamAbortCutsAnAnswerTheTargetGaveAnyway(t *testing.T) {
	// A target may answer whatever it read of the body: a step that cut what the mock sent on still cuts the client's
	// answer, and the target's is closed, so its connection is let go.
	closed := make(chan struct{})
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(r.Body)
		body := closedBody{Reader: strings.NewReader("answer"), closed: closed}

		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, ContentLength: -1, Request: r}, nil
	})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Postf("/up").UpstreamStep(limitStep{n: 2, err: vlatte.ErrAbort}).Reply(From("http://target.test").Transport(transport)))

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	_, err := client.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.ErrorIs(t, err, io.EOF)
	require.EqualValues(t, 1, scoped.Hits())

	select {
	case <-closed:
	default:
		require.Fail(t, "the target's answer stayed open")
	}
}

// holdStep holds what its stage gets until d passed, or the flow ends.
type holdStep struct{ d time.Duration }

func (s holdStep) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return vlatte.StepFunc(func(ctx context.Context, b []byte, next vlatte.Stage) (int, error) {
		select {
		case <-time.After(s.d):
			return next.Write(b)
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}).Open(flow, next)
}

func TestStepReader_CloseStopsTheSteps(t *testing.T) {
	// A target that answered early, before it read the whole body, has the transport close it: a stage that holds the
	// body on a timer doesn't keep the call waiting.
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		r := newStepReader(t.Context(), io.NopCloser(strings.NewReader("ping")), []vlatte.Step{holdStep{d: time.Hour}})

		require.NoError(t, r.Close())
		require.Zero(t, time.Since(start))
	})
}

func TestProxyReply_AnUpstreamCutEndsTheBodyStepsAtOnce(t *testing.T) {
	// The cut is the answer: the steps of the body end as for any cut, dropping what they hold, rather than send it on
	// their own time first.
	target := receivingTarget(t, make(chan upstreamReceived, 1))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	closed := make(chan error, 1)
	m.MustMock(Postf("/up").Step(closeStep{closed: closed}).UpstreamStep(limitStep{n: 2, err: vlatte.ErrAbort}).Reply(From(target.URL)))

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	_, err := client.Post(m.URL("/up"), MIMETextPlain, strings.NewReader("ping"))
	require.ErrorIs(t, err, io.EOF)
	require.ErrorIs(t, <-closed, vlatte.ErrAbort)
}

// countingReader counts the bytes read of r.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))

	return n, err
}

func TestStepReader_TakesTheBodyOnlyAsTheTargetReadsIt(t *testing.T) {
	// A body the server didn't read whole is still the client's: what the steps take of it, the client sent. They
	// take nothing before the target reads, then a small piece at a time, so a stage that holds one holds the upload.
	synctest.Test(t, func(t *testing.T) {
		body := &countingReader{r: strings.NewReader(strings.Repeat("x", 64<<10))}
		r := newStepReader(t.Context(), io.NopCloser(body), []vlatte.Step{holdStep{d: time.Hour}})

		synctest.Wait()
		require.Zero(t, body.n.Load(), "taken before the target reads")

		go func() { _, _ = r.Read(make([]byte, 1)) }()

		synctest.Wait()
		require.EqualValues(t, _upstreamPiece, body.n.Load())
		require.NoError(t, r.Close())
	})
}

func TestStepReader_CloseEndsTheStepsOfABodyNeverRead(t *testing.T) {
	// A target that answers without reading the body, or a call that fails first, has the transport close it: the
	// steps end, as for a body read, and nothing of the body is taken.
	body := &countingReader{r: strings.NewReader("ping")}
	closed := make(chan error, 1)
	r := newStepReader(t.Context(), io.NopCloser(body), []vlatte.Step{closeStep{closed: closed}})

	require.NoError(t, r.Close())
	require.ErrorIs(t, <-closed, errBodyEnded)
	require.Zero(t, body.n.Load())
}

func TestStepReader_CloseEndsTheCopy(t *testing.T) {
	// The transport closes a request body it no longer reads, such as when the target answered early: the copy
	// through the steps ends, rather than wait on the pipe.
	body := io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20)))
	r := newStepReader(t.Context(), body, []vlatte.Step{appendStep("")})

	first := make([]byte, 4)
	_, err := io.ReadFull(r, first)
	require.NoError(t, err)

	require.NoError(t, r.Close())

	_, err = r.Read(first)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestProxyReply_MappersOfABodyThatDoesntEndFail(t *testing.T) {
	// #61: mappers read the whole body of the target first, with no limit: for an event stream, which doesn't end, the
	// client got no answer, and the stream piled up in memory. An event stream fails at once, and a body larger than
	// the proxy keeps fails once it is read past that size. A mock without mappers still streams.
	release := make(chan struct{})
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		http.NewResponseController(w).Flush()
		<-release
	}))
	t.Cleanup(stream.Close)
	t.Cleanup(func() { close(release) })

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("l", _keptBodyLimit+1))
	}))
	t.Cleanup(large.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	mapper := func(*RequestValues, *Response) error { return nil }
	m.MustMock(
		Getf("/plain").Reply(From(stream.URL)),
		Getf("/mapped").Map(mapper).Reply(From(stream.URL)),
		Getf("/large").Map(mapper).Reply(From(large.URL)),
	)

	client := &http.Client{Timeout: 2 * time.Second}

	res, err := client.Get(m.URL("/plain"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	line, err := bufio.NewReader(res.Body).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", line)

	for path, want := range map[string]string{
		"/mapped": "an event stream doesn't end",
		"/large":  fmt.Sprintf("the target's is over %d bytes", _keptBodyLimit),
	} {
		res, err := client.Get(m.URL(path))
		require.NoError(t, err, path)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode, path)
		require.Contains(t, string(body), want, path)
	}
}

func TestProxyReply_MappersReadBodyFilesWhole(t *testing.T) {
	// The limit on what mappers read is for proxied bodies, of unknown size: a body file is read whole.
	dir := t.TempDir()
	content := strings.Repeat("f", _keptBodyLimit+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "large.txt"), []byte(content), 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/file").
		Map(func(_ *RequestValues, res *Response) error { res.Body = append(res.Body, '!'); return nil }).
		Reply(OK().BodyFile(filepath.Join(dir, "large.txt"))))

	status, body := get(t, m, "/file")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, content+"!", body)
}

func TestProxyReply_AClientThatLeavesDuringTheDelayIsNoError(t *testing.T) {
	// #62: a client that left during a mock's delay had the proxy call the target with its ended context, which failed,
	// and was logged as an error. Nothing failed but the client's wait: the mock doesn't reply, and the log warns.
	var calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(target.Close)

	logs := &lockedBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false))
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Getf("/p").Delay(300 * time.Millisecond).Reply(From(target.URL)))

	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := client.Get(m.URL("/p"))
	require.Error(t, err)

	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "the client left during the delay") },
		2*time.Second, 10*time.Millisecond)

	require.Zero(t, calls.Load(), "the target was called")
	require.NotContains(t, logs.String(), `"level":"error"`)
	require.False(t, scoped.Called())
}

// lockedBuffer is a buffer a logger writes to while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestProxyReply_AMockFileTargetThatIsntHTTPFailsOnItsKey(t *testing.T) {
	// #63: a mock file's proxy.target had to have a scheme and a host, any scheme: ftp://files.local passed, and failed
	// only when the mock was built, with an error of the Go API that names no key.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	_, err := m.Mock(FromBytes([]byte("request:\n  path: /p\nproxy:\n  target: ftp://files.local\n"), "yaml"))
	require.ErrorContains(t, err, "proxy.target")
	require.ErrorContains(t, err, "expected an absolute http or https URL, such as http://api.local:8080")
	require.NotContains(t, err.Error(), "reply_proxy:")
}

func TestProxyReply_SendsTrailersTheTargetDidntAnnounce(t *testing.T) {
	// A target can send a trailer it didn't announce in a Trailer header. The transport adds those to the response's
	// trailers once the body is read: the proxy must hand it a map to add them to, or they never reach the client.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "body")
		http.NewResponseController(w).Flush()
		w.Header().Set(http.TrailerPrefix+"X-Late", "late")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(target.URL)))

	for name, get := range map[string]func() (*http.Response, error){
		"a proxy reply":     func() (*http.Response, error) { return http.Get(m.URL("/p")) },
		"the forward proxy": func() (*http.Response, error) { return proxyClient(t, m).Get(target.URL + "/x") },
	} {
		t.Run(name, func(t *testing.T) {
			res, err := get()
			require.NoError(t, err)

			_, err = io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, "late", res.Trailer.Get("X-Late"))
		})
	}
}

func TestProxyReply_ACallADeadlineEndsGetsTheFailureAnswer(t *testing.T) {
	// A client that left gets no answer, and its failure is a warning. A request whose context a middleware's deadline
	// ended still has its client: it gets the server's answer to the failure, not an empty 200.
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(target.Close)
	t.Cleanup(func() { close(release) })

	m := MustNew(Setup().Silent().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/slow").Reply(From(target.URL)))

	status, body := get(t, m, "/slow")
	require.Equal(t, m.Config().ErrorStatusCode, status)
	require.Contains(t, body, "context deadline exceeded")
}

func TestTargetBody_KeepsNothingUnlessToldTo(t *testing.T) {
	// A proxy kept up to _keptBodyLimit of every body it streamed, though nothing read it: a server that proxies many
	// bodies at once held that much for each. It keeps a body only for what reads it once it is sent.
	for _, keep := range []bool{false, true} {
		body := &targetBody{ReadCloser: io.NopCloser(strings.NewReader("streamed")), cancel: func(error) {}, keep: keep}

		_, err := io.ReadAll(body)
		require.NoError(t, err)

		kept, ok := body.Kept()
		require.Equal(t, keep, ok)

		if keep {
			require.Equal(t, "streamed", string(kept))
		} else {
			require.Empty(t, kept)
		}
	}
}

func TestProxyReply_AClientThatLeavesDuringTheCallIsNoError(t *testing.T) {
	// #62: a client that left while the target was still to answer ended the proxy's call, which failed, and was
	// logged as an error. Nothing failed but the client's wait: the log warns, and the client gets nothing.
	called := make(chan struct{}, 1)
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
		<-release
	}))
	t.Cleanup(target.Close)
	t.Cleanup(func() { close(release) })

	logs := &lockedBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(target.URL)))

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/p"), nil)
	require.NoError(t, err)

	go func() {
		<-called
		cancel()
	}()

	_, err = http.DefaultClient.Do(req)
	require.ErrorIs(t, err, context.Canceled)

	require.Eventually(t, func() bool { return strings.Contains(logs.String(), `"level":"warn"`) },
		2*time.Second, 10*time.Millisecond)
	require.Contains(t, logs.String(), "calling the target "+target.URL+": context canceled")
	require.NotContains(t, logs.String(), `"level":"error"`)
}

// lateTransport answers once the request's context ended, as a transport that doesn't heed it may, with a body that
// reports its close.
type lateTransport struct {
	closed chan struct{}
}

func (rt lateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       closeSignal{Reader: strings.NewReader("late"), closed: rt.closed},
		Request:    r,
	}, nil
}

func TestProxyReply_ATimedOutAnswerThatComesAnywayIsClosed(t *testing.T) {
	// A transport of the caller's may answer after the timeout ended the call: the client gets the timeout, and the
	// late answer's body is closed, or its connection stays taken.
	closed := make(chan struct{})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From("http://target.local").Transport(lateTransport{closed: closed}).Timeout(50 * time.Millisecond)))

	status, body := get(t, m, "/p")
	require.Equal(t, m.Config().ErrorStatusCode, status)
	require.Contains(t, body, "calling the target http://target.local: no response headers within 50ms")

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the late answer's body is still open")
	}
}

// roundTripFunc is a transport of a func.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProxyReply_ABodyReadWholeCanBeSentAgain(t *testing.T) {
	// #69: the forwarded request had no GetBody, so its transport couldn't send its body again when a kept-alive
	// connection to the target turned out closed, as it does for replayable requests, such as with an Idempotency-Key.
	// The server reads a body under MaxBodyParsingLimit whole anyway: it can be read again, an empty one too. A larger
	// one can't.
	target := okTarget(t)

	// The transport runs on the server's goroutine: it reports what it got, for the test to check.
	replayed := make(chan string, 1)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.GetBody == nil {
			replayed <- "<no GetBody>"
			return http.DefaultTransport.RoundTrip(r)
		}

		body, err := r.GetBody()
		if err != nil {
			replayed <- "<GetBody failed>"
			return nil, err
		}

		b, err := io.ReadAll(body)
		replayed <- string(b)

		if err != nil {
			return nil, err
		}

		return http.DefaultTransport.RoundTrip(r)
	})

	m := MustNew(Setup().Silent().MaxBodyParsingLimit(8))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Postf("/p").Reply(From(target.URL).Transport(transport)))

	for body, want := range map[string]string{"short": "short", "": "", "longer than 8": "<no GetBody>"} {
		req, err := http.NewRequest(http.MethodPost, m.URL("/p"), strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Idempotency-Key", "k")

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, want, <-replayed, body)
	}
}

func TestProxyReply_TellsTheTargetAboutTheClientWhenAsked(t *testing.T) {
	// #69: the target couldn't tell the client's address, scheme or host. With XForwarded, or x_forwarded in a mock
	// file, it gets them in X-Forwarded-For, after the addresses of the proxies before, X-Forwarded-Host and
	// X-Forwarded-Proto. With PreserveHost, or preserve_host, the target gets the Host the client asked for. Without
	// them, it gets the client's headers as they came, and its own host.
	type sent struct {
		host   string
		header http.Header
	}

	got := make(chan sent, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- sent{host: r.Host, header: r.Header.Clone()}
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(
		Getf("/plain").Reply(From(target.URL)),
		Getf("/go").Reply(From(target.URL).XForwarded().PreserveHost()),
		FromBytes([]byte("request: {path: /file}\nproxy: {target: '"+target.URL+"', x_forwarded: true, preserve_host: true}\n"), "yaml"),
	)

	clientHost := hostOf(t, m)
	targetHost := target.Listener.Addr().String()

	call := func(path string) sent {
		req, err := http.NewRequest(http.MethodGet, m.URL(path), nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "10.0.0.1")

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return <-got
	}

	plain := call("/plain")
	require.Equal(t, targetHost, plain.host)
	require.Equal(t, "10.0.0.1", plain.header.Get("X-Forwarded-For"))
	require.Empty(t, plain.header.Get("X-Forwarded-Host"))
	require.Empty(t, plain.header.Get("X-Forwarded-Proto"))

	for _, path := range []string{"/go", "/file"} {
		asked := call(path)
		require.Equal(t, clientHost, asked.host, path)
		require.Equal(t, "10.0.0.1, 127.0.0.1", asked.header.Get("X-Forwarded-For"), path)
		require.Equal(t, clientHost, asked.header.Get("X-Forwarded-Host"), path)
		require.Equal(t, "http", asked.header.Get("X-Forwarded-Proto"), path)
	}
}

func TestProxyReply_FromTakesAStringTypeOfAnyName(t *testing.T) {
	// #69: FromTypes took no ~string, so a target of a named string type, such as a setting's, didn't compile.
	type targetURL string

	target := okTarget(t)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/p").Reply(From(targetURL(target.URL))))

	status, body := get(t, m, "/p")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", body)
}

func TestProxyReply_AnEmptyBodyGoesOnAsNone(t *testing.T) {
	// An empty request body went on as a body of unknown length: the target got it chunked, which some targets refuse,
	// as a POST without a Content-Length. It goes on as none, with a Content-Length of 0.
	type sent struct {
		length   int64
		encoding []string
	}

	got := make(chan sent, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- sent{length: r.ContentLength, encoding: r.TransferEncoding}
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Postf("/p").Reply(From(target.URL)))

	res, err := http.Post(m.URL("/p"), "text/plain", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	s := <-got
	require.Zero(t, s.length)
	require.Empty(t, s.encoding)
}

func TestProxyReply_ASwitchWithoutAConnectionFails(t *testing.T) {
	// A transport of the caller's may answer a switch of protocols with a 101 whose body isn't the connection to the
	// target: there is nothing to pipe the client's connection to, and the client gets the server's answer to a
	// failure, saying why.
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusSwitchingProtocols,
			Header:     http.Header{HeaderConnection: {"Upgrade"}, HeaderUpgrade: {"echo"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/ws").Reply(From("http://target.local").Transport(transport)))

	_, _, res := switchThrough(t, m, "/ws")

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Contains(t, string(body), "the target http://target.local switched protocols, and the transport gave no connection to it")
}

func TestProxyReply_ADescribedReplyLoadsBackAsTheSame(t *testing.T) {
	// A proxy reply's description loads back as the same reply: the target gets the same request from the mock it
	// loads as from the one described, with its trim, its request headers, and the client's details and host.
	type sent struct {
		host   string
		uri    string
		header http.Header
	}

	got := make(chan sent, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- sent{host: r.Host, uri: r.RequestURI, header: r.Header.Clone()}
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/api/items").Reply(From(target.URL).
		TrimPrefix("/api").
		RequestHeader("X-Added", "a").
		RemoveRequestHeaders("X-Removed").
		XForwarded().
		PreserveHost()))

	call := func() sent {
		req, err := http.NewRequest(http.MethodGet, m.URL("/api/items"), nil)
		require.NoError(t, err)
		req.Header.Set("X-Removed", "r")

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		return <-got
	}

	described := call()
	require.Equal(t, "/items", described.uri)
	require.Equal(t, hostOf(t, m), described.host)
	require.Equal(t, "a", described.header.Get("X-Added"))
	require.Empty(t, described.header.Get("X-Removed"))
	require.Equal(t, hostOf(t, m), described.header.Get("X-Forwarded-Host"))

	content, err := scope.Mocks()[0].MarshalJSON()
	require.NoError(t, err)
	require.NoError(t, m.Clean(t.Context()))
	m.MustMock(FromBytes(content, "json"))

	require.Equal(t, described, call(), "the loaded mock sent another request")
}
