package e2e

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxiedReplies(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "ok", r.Header.Get("x-test"))
		assert.Equal(t, "", r.Header.Get("x-del"))
		assert.Equal(t, mockhttp.MIMETextPlain, r.Header.Get(mockhttp.HeaderContentType))

		b, err := io.ReadAll(r.Body)
		if err != nil && errors.Is(err, io.EOF) {
			require.NoError(t, err)
		}

		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}))

	defer dest.Close()

	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(mockhttp.Post(match.URLPath("/test")).
		Body(match.StrictEqual("hello world")).
		Reply(mockhttp.From(dest.URL).
			RequestHeader("x-test", "ok").
			Header("x-res", "example").
			RemoveRequestHeaders("x-del")))

	data := strings.NewReader("hello world")
	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", data)
	req.Header.Add("x-del", "to-delete")
	req.Header.Add(mockhttp.HeaderContentType, mockhttp.MIMETextPlain)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.AssertCalled(t))
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "example", res.Header.Get("x-res"))
	assert.Equal(t, "hello world", string(b))
}

func TestProxiedReplyMockFileTrimSuffix(t *testing.T) {
	target := mockhttp.MustNew()
	target.MustStart()
	defer target.Close()

	targetScoped := target.MustMock(mockhttp.Getf("/items").Reply(mockhttp.OK()))

	m := mockhttp.MustNew()
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.FromBytes(forwardingTo(t, "testdata/response_forwarded/trim_suffix.yaml", target.URL()), "yaml"))

	res, err := http.Get(m.URL("/items.json"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	// trim_suffix removes ".json" before forwarding, so the target is called with /items.
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.True(t, targetScoped.AssertCalled(t))
}

func TestProxiedReplyMockFile(t *testing.T) {
	target := mockhttp.MustNew()
	target.MustStart()
	defer target.Close()

	targetScoped := target.MustMock(
		mockhttp.Postf("/test").
			Headerf("test", "ok").
			Header("del", match.Not(match.Present())).
			Reply(mockhttp.OK().PlainText("done")))

	m := mockhttp.MustNew()
	m.MustStart()
	defer m.Close()

	scoped := m.MustMock(mockhttp.FromBytes(forwardingTo(t, "testdata/response_forwarded/proxied_response.yaml", target.URL()), "yaml"))
	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", strings.NewReader("hello world"))
	req.Header.Add(mockhttp.HeaderContentType, mockhttp.MIMETextPlain)
	req.Header.Add("del", "to be deleted")

	res, err := httpClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "done", string(b))
	require.True(t, targetScoped.AssertCalled(t))
	require.True(t, scoped.AssertCalled(t))
}

// forwardingTo reads a mock file that forwards to http://target.invalid, and points it at target instead. A mock
// file is data, so a value known only when the test runs goes in before the file is built.
func forwardingTo(t *testing.T, file, target string) []byte {
	t.Helper()

	doc, err := os.ReadFile(file)
	require.NoError(t, err)

	return bytes.ReplaceAll(doc, []byte("http://target.invalid"), []byte(target))
}

func TestProxiedReplies_TheFragmentStaysHere(t *testing.T) {
	// HTTP requests carry no fragment: forwarded, /test#contacts goes as /test, not as the path "/test#contacts".
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.RequestURI))
	}))
	t.Cleanup(dest.Close)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Request().URLFragmentf("contacts").Reply(mockhttp.From(dest.URL)))

	res, body := getTarget(t, m, "/test#contacts")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "/test", string(body))
}

func TestProxiedReplyMockFileTellsTheTargetAboutTheClient(t *testing.T) {
	// A mock file's proxy reply trims whole segments, and with x_forwarded and preserve_host, the target gets the
	// client's details and the Host it asked for.
	got := make(chan *http.Request, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got <- r }))
	t.Cleanup(target.Close)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromBytes(forwardingTo(t, "testdata/response_forwarded/client_details.yaml", target.URL), "yaml"))

	res, err := http.Get(m.URL("/api/items"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	host := strings.TrimPrefix(m.URL(), "http://")
	r := <-got
	require.Equal(t, "/items", r.URL.Path)
	require.Equal(t, host, r.Host)
	require.Equal(t, "127.0.0.1", r.Header.Get("X-Forwarded-For"))
	require.Equal(t, host, r.Header.Get("X-Forwarded-Host"))
	require.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
}
