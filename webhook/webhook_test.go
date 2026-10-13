package webhook_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

const (
	_tlsCertFile       = "./testdata/cert/cert.pem"
	_tlsKeyFile        = "./testdata/cert/key.pem"
	_tlsClientCertFile = "./testdata/cert/cert_client.pem"
	_tlsClientKeyFile  = "./testdata/cert/key_client.pem"
)

func TestWebHook_Run(t *testing.T) {
	name := "webhook"
	key := "test_key"
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	testCases := []struct {
		name          string
		targetMock    *mockhttp.Builder
		webhookDef    vlatte.EventConfig
		options       []webhook.Option
		expectedCalls int64
	}{
		{
			name:          "basic with default method",
			targetMock:    mockhttp.Getf("/third_party/hook").Reply(mockhttp.OK()),
			webhookDef:    webhook.Options().URL(target.URL("/third_party/hook")).Build(name),
			expectedCalls: 1,
		},
		{
			name: "complex",
			targetMock: mockhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body(match.Eq("hi")).
				Reply(mockhttp.OK().PlainText("bye")),
			webhookDef: webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body("hi").
				Build(name),
			expectedCalls: 1,
		},
		{
			name: "no body",
			targetMock: mockhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Reply(mockhttp.OK().PlainText("bye")),
			webhookDef: webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			expectedCalls: 1,
		},
		{
			name: "bad request from target",
			targetMock: mockhttp.Postf("/third_party/hook").
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Reply(mockhttp.BadRequest()),
			webhookDef: webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			expectedCalls: 1,
		},
		{
			name: "transform",
			targetMock: mockhttp.Putf("/third_party/hook/transformed").
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body(match.Eq("hello world")).
				Reply(mockhttp.BadRequest()),
			webhookDef: webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			options: []webhook.Option{
				webhook.WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *webhook.Args) error {
					webhookArgs.URL += "/transformed"
					webhookArgs.Method = http.MethodPut
					webhookArgs.Body = "hello world"
					return nil
				}),
			},
			expectedCalls: 1,
		},
		{
			name:       "transform with error",
			targetMock: mockhttp.Getf("/third_party/hook/transformed").Reply(mockhttp.BadRequest()),
			webhookDef: webhook.Options().URL(target.URL("/third_party/hook")).Method(http.MethodGet).Build(name),
			options: []webhook.Option{
				webhook.WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *webhook.Args) error {
					return errors.New("boom")
				}),
			},
			expectedCalls: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New(tc.options...)
			require.NoError(t, err)

			m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustMock(tc.targetMock)

			m.MustMock(mockhttp.Getf("/test").
				OnCompleteEvent(tc.webhookDef).
				Reply(mockhttp.NoContent()))

			client := &http.Client{}
			req, _ := http.NewRequest(http.MethodGet, m.URL("/test"), nil)
			res, err := client.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			// The webhook is sent after the response. Before the target is checked, and cleaned for the next case, it
			// has been sent, or not.
			m.WaitEvents()
			require.True(t, target.AssertNumberOfCalls(t, tc.expectedCalls))
			require.True(t, m.AssertNumberOfCalls(t, 1))
		})
	}
}

func TestWebHook_TLS(t *testing.T) {
	name := "webhook"
	key := "test_key"
	cert, err := tls.LoadX509KeyPair(_tlsClientCertFile, _tlsClientKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	whSrv := mockhttp.MustNew(mockhttp.Setup().Silent().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	whSrv.MustStartTLS()
	defer whSrv.Close()

	whSrv.MustMock(mockhttp.Postf("/third_party/hook/tls").
		Headerf("X-Key", "%s", key).
		Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlainCharsetUTF8).
		Reply(mockhttp.OK().PlainText("hello")))

	wh, err := webhook.New(webhook.WithRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
		},
	}))
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().
		Silent().
		TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile).
		OnCompleteListener(name, wh))
	m.MustStartTLS()
	defer m.Close()

	m.MustMock(mockhttp.Putf("/test").
		OnCompleteEvent(webhook.Options().
			URL(whSrv.URL("/third_party/hook/tls")).
			Method(http.MethodPost).
			Header("X-Key", key).
			Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlainCharsetUTF8).
			Body("hi").
			Build(name)).
		Reply(mockhttp.NoContent()))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
			},
		},
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, m.URL("/test"), nil)
	require.NoError(t, err)

	res, err := client.Do(req)
	require.NoError(t, err)

	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	m.WaitEvents()
	require.True(t, whSrv.AssertNumberOfCalls(t, 1))
	require.True(t, m.AssertNumberOfCalls(t, 1))
}

func TestWebHook_DoesNotDelayTheResponse(t *testing.T) {
	// #15: the webhook was sent inside the handler, so the client waited for the target, up to the webhook timeout. The
	// response comes first now, and the webhook after it.
	unblock := make(chan struct{})
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-unblock:
		case <-r.Context().Done(): // the webhook canceled, when the test fails first
			return
		}

		got <- r.Method + " " + r.URL.Path
	}))
	t.Cleanup(target.Close)

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(webhook.Options().URL(target.URL + "/hook").Method(http.MethodPost).Build("webhook")).
		Reply(mockhttp.NoContent()))

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(m.URL("/test"))
	require.NoError(t, err, "the response doesn't wait for the target, which never answers until the end")
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	close(unblock)
	m.WaitEvents()
	require.Equal(t, "POST /hook", <-got)
}

func TestWebHook_InvalidArgs(t *testing.T) {
	// Parameters that are neither *Args nor a map fail the mock when it is built, not each request after it.
	name := "webhook"

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener(name, wh))
	defer m.Close()

	testCases := []struct {
		name   string
		params vlatte.Parameters
		want   string
	}{
		{"another type", make(chan struct{}), "webhook: arguments must be *Args or map[string]any"},
		{"nil", nil, "webhook: arguments must not be nil"},
		{"nil *Args", (*webhook.Args)(nil), "webhook: arguments must not be nil"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(mockhttp.Getf("/test").
				OnCompleteEvent(vlatte.EventConfig{Name: name, Parameters: tc.params}).
				Reply(mockhttp.NoContent()))

			require.ErrorContains(t, err, "mock: on_complete listener webhook at index 0: "+tc.want)
		})
	}
}

func TestWebHook_FileSetup(t *testing.T) {
	name := "webhook"
	target := mockhttp.MustNew(mockhttp.Setup())
	target.MustStart()
	defer target.Close()

	target.MustMock(mockhttp.Postf("/fs/hook").
		Headerf("hello", "world").
		Headerf("dev", "ok").
		ContentType(mockhttp.MIMEApplicationJSON).
		Body(match.Eq(`{"task": "done"}`)).
		Reply(mockhttp.NoContent()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener(name, wh))
	m.SetData(map[string]any{"webhook_target": target.URL("/fs/hook")})
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.FromFile("testdata/1_webhook_complete_setup.yaml"))

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, m.URL("/test"), nil)
	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	m.WaitEvents()
	require.True(t, target.AssertNumberOfCalls(t, 1))
	require.True(t, m.AssertNumberOfCalls(t, 1))
}

func TestWebHook_InvalidFiles(t *testing.T) {
	name := "webhook"
	testCases := []struct {
		name     string
		filename string
	}{
		{"on_complete must be an array", "testdata/2_webhook_object_not_array.yaml"},
		{"item is not an object", "testdata/3_webhook_invalid_item_type.yaml"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New()
			require.NoError(t, err)

			m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener(name, wh))
			defer m.Close()

			mock, err := m.Mock(mockhttp.FromFile(tc.filename))
			// The error must come from the on_complete definition itself, not from on_complete being unknown.
			require.ErrorContains(t, err, "invalid_value: on_complete")
			require.Nil(t, mock)
		})
	}
}

func TestWebHook_DefaultTransportHasTimeouts(t *testing.T) {
	wh, err := webhook.New()
	require.NoError(t, err)

	tr, ok := wh.RoundTripper.(*http.Transport)
	require.True(t, ok)
	// A bare http.Transport has no dial or TLS handshake timeouts: a stuck target could block forever.
	require.NotNil(t, tr.DialContext)
	require.NotZero(t, tr.TLSHandshakeTimeout)
}

func TestWebHook_Timeout(t *testing.T) {
	// The timeout bounds a send: a target that never answers fails it after exactly the timeout.
	synctest.Test(t, func(t *testing.T) {
		wh, err := webhook.New(webhook.WithRoundTripper(silent{}), webhook.WithTimeout(time.Minute))
		require.NoError(t, err)

		start := time.Now()
		err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test/slow"}})

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, time.Minute, time.Since(start))
	})
}

// silent is a RoundTripper whose target never answers: it returns when the request's context ends.
type silent struct{}

func (silent) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestWebHook_Templates(t *testing.T) {
	name := "webhook"
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	// Templates render with the request that completed the mock: PUT /orders/42.
	testCases := []struct {
		name       string
		targetMock *mockhttp.Builder
		parameters map[string]any
	}{
		{
			"url_template",
			mockhttp.Postf("/hooks/42").Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url_template": target.URL() + "/hooks/{{ index .Request.URLPathSegments 1 }}"},
		},
		{
			"header_template",
			mockhttp.Postf("/hooks").Headerf("X-Method", "PUT").Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "header_template": map[string]any{"X-Method": "{{ .Request.Method }}"}},
		},
		{
			"body_template",
			mockhttp.Postf("/hooks").Body(match.Eq("order 42")).Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "body_template": "order {{ index .Request.URLPathSegments 1 }}"},
		},
		{
			"body_file",
			mockhttp.Postf("/hooks").Body(match.Eq("from a file\n")).Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "body_file": "testdata/hook_body.txt"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New()
			require.NoError(t, err)

			m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustMock(tc.targetMock)
			m.MustMock(mockhttp.Putf("/orders/42").
				OnCompleteEvent(vlatte.EventConfig{Name: name, Parameters: tc.parameters}).
				Reply(mockhttp.NoContent()))

			req, _ := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
			res, err := http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			m.WaitEvents()
			require.True(t, target.AssertNumberOfCalls(t, 1))
		})
	}
}

// onComplete is a minimal event to call WebHook.Run directly.
type onComplete struct {
	params vlatte.Parameters
	mock   vlatte.Mock
	kit    vlatte.Kit
	logger *zerolog.Logger
}

func (e onComplete) Kind() vlatte.Kind             { return mockhttp.Kind }
func (e onComplete) Kit() vlatte.Kit               { return e.kit }
func (e onComplete) Parameters() vlatte.Parameters { return e.params }
func (e onComplete) Mock() vlatte.Mock             { return e.mock }
func (e onComplete) Logger() *zerolog.Logger       { return e.logger }

// answer is a RoundTripper that keeps the requests it is given and answers each with status and a body.
type answer struct {
	status int
	reqs   []*http.Request
}

func (a *answer) RoundTrip(req *http.Request) (*http.Response, error) {
	a.reqs = append(a.reqs, req)

	return &http.Response{
		StatusCode: a.status,
		Status:     strconv.Itoa(a.status) + " " + http.StatusText(a.status),
		Header:     http.Header{"Location": {"/elsewhere"}},
		Body:       io.NopCloser(strings.NewReader("answer")),
		Request:    req,
	}, nil
}

// logs collects log lines, safe for the goroutines of a server.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// kit is the Kit of a server without a template engine: templates render with data.
type kit struct {
	data any
}

func (k kit) TemplateEngine() vlatte.TemplateEngine { return nil }
func (k kit) Data() any                             { return k.data }

// sent is a RoundTripper that keeps the request it is given and answers 200.
type sent struct {
	req *http.Request
}

func (s *sent) RoundTrip(req *http.Request) (*http.Response, error) {
	s.req = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

func TestWebHook_TemplateErrors(t *testing.T) {
	wh, err := webhook.New()
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url_template": "http://localhost/{{ .Nope"}})
	require.ErrorIs(t, err, webhook.ErrRenderingTemplate)
}

func TestWebHook_KitWithoutAnEngine(t *testing.T) {
	// A Kit without a template engine renders the event's templates with Go templates, and the Kit's data.
	rt := &sent{}
	wh, err := webhook.New(webhook.WithRoundTripper(rt))
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url_template": "http://hooks.test/{{ .id }}"}, kit: kit{data: map[string]any{"id": "42"}}})
	require.NoError(t, err)
	require.Equal(t, "/42", rt.req.URL.Path)
}

func TestWebHook_HeaderTemplateValues(t *testing.T) {
	// As in replies: a rendered header value goes without the line breaks around it, such as the one a YAML block
	// scalar ends with, and a line break inside fails the send before the request is made, whatever the RoundTripper
	// would do with it.
	rt := &sent{}
	wh, err := webhook.New(webhook.WithRoundTripper(rt))
	require.NoError(t, err)

	send := func(v string) error {
		return wh.Run(t.Context(), onComplete{
			params: map[string]any{"url": "http://hooks.test", "header_template": map[string]any{"X-A": "{{ .v }}"}},
			kit:    kit{data: map[string]any{"v": v}},
		})
	}

	require.NoError(t, send("GET\n"))
	require.Equal(t, []string{"GET"}, rt.req.Header.Values("X-A"))

	rt.req = nil
	require.EqualError(t, send("a\nX-B: b"), "webhook: header_template X-A holds a line break")
	require.Nil(t, rt.req)
}

func TestWebHook_AnAnswerOutside2xxFailsTheSend(t *testing.T) {
	// A target's 4xx or 5xx was only a warning of the webhook's own logger, which discarded it by default, and a
	// redirect counted as a success. Each fails the send now, and the server logs it; a redirect isn't followed, since a
	// POST would turn into a GET.
	tests := []struct {
		status int
		want   string
	}{
		{status: http.StatusOK},
		{status: http.StatusNoContent},
		{status: http.StatusFound, want: "webhook: POST http://hooks.test/x: the target answered 302 Found"},
		{status: http.StatusBadRequest, want: "webhook: POST http://hooks.test/x: the target answered 400 Bad Request"},
		{status: http.StatusInternalServerError, want: "webhook: POST http://hooks.test/x: the target answered 500 Internal Server Error"},
	}

	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			rt := &answer{status: tc.status}
			wh, err := webhook.New(webhook.WithRoundTripper(rt))
			require.NoError(t, err)

			err = wh.Run(t.Context(), onComplete{params: map[string]any{"method": "POST", "url": "http://hooks.test/x"}})
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.want)
			}

			require.Len(t, rt.reqs, 1, "one request, a redirect included")
		})
	}
}

func TestWebHook_SendsWhatTheURLAndMethodSay(t *testing.T) {
	// method: post went out as "post", though request methods match in any case, and the user and password of a URL
	// were never sent: RoundTrip, unlike http.Client, doesn't send them as Basic auth.
	rt := &sent{}
	wh, err := webhook.New(webhook.WithRoundTripper(rt))
	require.NoError(t, err)

	require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"method": "post", "url": "http://user:pa%20ss@hooks.test/x"}}))
	require.Equal(t, http.MethodPost, rt.req.Method)

	user, password, ok := rt.req.BasicAuth()
	require.True(t, ok)
	require.Equal(t, "user", user)
	require.Equal(t, "pa ss", password)

	// As with http.Client, an Authorization header wins.
	params := map[string]any{"url": "http://user:pass@hooks.test/x", "header": map[string]any{"Authorization": "Bearer t"}}
	require.NoError(t, wh.Run(t.Context(), onComplete{params: params}))
	require.Equal(t, "Bearer t", rt.req.Header.Get("Authorization"))
}

func TestWebHook_LogsEachSendOnce(t *testing.T) {
	// A failed send was logged by the webhook, then returned to the server, which logged it again. The webhook logs
	// the send and its answer at info level now, and returns failures for the server to log. Logs keep a URL's
	// password out.
	tests := []struct {
		status int
		want   []string
	}{
		{status: http.StatusOK, want: []string{"---> WEBHOOK REQUEST GET /x", "<--- WEBHOOK GET /x"}},
		{status: http.StatusInternalServerError, want: []string{"---> WEBHOOK REQUEST GET /x"}},
	}

	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			out := &logs{}
			logger := zerolog.New(out)

			wh, err := webhook.New(webhook.WithRoundTripper(&answer{status: tc.status}), webhook.WithLogger(&logger))
			require.NoError(t, err)

			_ = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://user:secret@hooks.test/x"}})

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			require.Len(t, lines, len(tc.want), out.String())

			for i, want := range tc.want {
				require.Contains(t, lines[i], `"level":"info"`)
				require.Contains(t, lines[i], want)
				require.Contains(t, lines[i], `"url":"http://user:xxxxx@hooks.test/x"`)
			}

			require.NotContains(t, out.String(), "secret")
		})
	}
}

func TestWebHook_LogsWithTheServerLogger(t *testing.T) {
	// Without WithLogger, webhooks logged nothing, or wrote to stdout with WithLogLevel, whatever the server's logger
	// and Silent() said. They log with the logger of their event now: the server's, in mockhttp.
	out := &logs{}
	logger := zerolog.New(out)

	rt := &sent{}
	wh, err := webhook.New(webhook.WithRoundTripper(rt))
	require.NoError(t, err)

	require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test/direct"}, logger: &logger}))
	require.Contains(t, out.String(), "<--- WEBHOOK GET /direct")

	m := mockhttp.MustNew(mockhttp.Setup().Logger(&logger).OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/x").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{"url": "http://hooks.test/server"}}).
		Reply(mockhttp.NoContent()))

	res, err := http.Get(m.URL("/x"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	m.WaitEvents()
	require.Contains(t, out.String(), "<--- WEBHOOK GET /server")
}

func TestWebHook_AZeroWebHookSends(t *testing.T) {
	// A WebHook built without New panicked: it had no Transform, RoundTripper or Logger, and no timeout.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Method + " " + r.URL.Path
	}))
	t.Cleanup(target.Close)

	var wh webhook.WebHook
	require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"method": "POST", "url": target.URL + "/x"}}))
	require.Equal(t, "POST /x", <-got)
}

func TestWithTimeout_RejectsANegativeTimeout(t *testing.T) {
	// A negative timeout made every send fail at once.
	_, err := webhook.New(webhook.WithTimeout(-time.Second))
	require.EqualError(t, err, "webhook: error applying option at index 0. reason: negative timeout -1s")
}

// clocked is a RoundTripper that keeps when it was called, and answers 204.
type clocked struct {
	at []time.Time
}

func (c *clocked) RoundTrip(req *http.Request) (*http.Response, error) {
	c.at = append(c.at, time.Now())
	return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: http.NoBody, Request: req}, nil
}

func TestWebHook_Delay(t *testing.T) {
	// A delay holds the send, as real webhooks often come some time after the response: it counts from the start of
	// the send, and the timeout from the request, after it. A context that ends during the delay sends nothing.
	t.Run("sent after the delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &clocked{}
			wh, err := webhook.New(webhook.WithRoundTripper(rt))
			require.NoError(t, err)

			start := time.Now()
			require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test", "delay": "1.5s"}}))

			require.Len(t, rt.at, 1)
			require.Equal(t, 1500*time.Millisecond, rt.at[0].Sub(start))
		})
	})

	t.Run("the timeout counts from the send", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			wh, err := webhook.New(webhook.WithRoundTripper(silent{}), webhook.WithTimeout(time.Second))
			require.NoError(t, err)

			start := time.Now()
			err = wh.Run(t.Context(), onComplete{params: &webhook.Args{URL: "http://hooks.test", Delay: 2 * time.Second}})

			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, 3*time.Second, time.Since(start), "the delay, then the timeout")
		})
	})

	t.Run("a context that ends during the delay sends nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &clocked{}
			wh, err := webhook.New(webhook.WithRoundTripper(rt))
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			err = wh.Run(ctx, onComplete{params: map[string]any{"url": "http://hooks.test", "delay": "1m"}})

			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.EqualError(t, err, "webhook: GET http://hooks.test: waiting for the delay of 1m0s: context deadline exceeded")
			require.Empty(t, rt.at)
		})
	})
}

func TestWebHook_DelayFromAMock(t *testing.T) {
	// The delay of a mock's webhook holds the webhook, not the response.
	got := make(chan time.Time, 1)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got <- time.Now() }))
	t.Cleanup(target.Close)

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/x").
		OnCompleteEvent(webhook.Options().URL(target.URL).Delay(200 * time.Millisecond).Build("webhook")).
		Reply(mockhttp.NoContent()))

	res, err := http.Get(m.URL("/x"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	answered := time.Now()

	m.WaitEvents()
	require.GreaterOrEqual(t, (<-got).Sub(answered), 150*time.Millisecond, "the webhook came after its delay")
}

func TestArgs_ADescriptionDecodesBack(t *testing.T) {
	// A mock's description holds its webhook's parameters as a mock file writes them, the ones set only: decoded, they
	// are the same parameters.
	tests := []struct {
		name string
		args *webhook.Args
	}{
		{
			name: "text keys",
			args: &webhook.Args{
				URL: "http://hooks.test/x", Method: "POST", Header: map[string]string{"X-A": "1"},
				HeaderTemplate: map[string]string{"X-B": "{{ .Request.Method }}"}, BodyTemplate: "{{ .Request.URL }}",
				Delay: 1500 * time.Millisecond,
			},
		},
		{
			name: "a url_template and a body file",
			args: &webhook.Args{URLTemplate: "http://hooks.test/{{ .Request.Method }}", BodyFile: "testdata/hook_body.txt"},
		},
		{
			name: "a body",
			args: &webhook.Args{URL: "http://hooks.test", Body: "paid"},
		},
		{
			name: "a multipart body",
			args: &webhook.Args{URL: "http://hooks.test", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart().BodyText("a")).Boundary("b")},
		},
		{
			name: "a multipart body whose text is templates",
			args: &webhook.Args{
				URL:       "http://hooks.test",
				Multipart: mockhttp.MultipartForm(mockhttp.NewPart().Name("n").BodyText("{{ .X }}")).Template(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desc, ok := tc.args.Describe().Props.(map[string]any)
			require.True(t, ok)

			decoded, err := webhook.ArgsFromMap(desc)
			require.NoError(t, err)
			require.Equal(t, desc, decoded.Describe().Props, "the description decodes to parameters described the same")

			if tc.args.Multipart == nil {
				require.Equal(t, tc.args, decoded)
			}
		})
	}

	_, templated := (&webhook.Args{Multipart: mockhttp.MultipartMixed().Template()}).Describe().Props.(map[string]any)["multipart_template"]
	require.True(t, templated, "a body whose text is templates goes under multipart_template")
}

func TestWebHook_ADescribedMockSendsTheSameWebhook(t *testing.T) {
	// Mock.Describe dropped on_complete events: a mock that went out of the admin API and back in lost its webhooks.
	// Described and loaded back, a mock sends the same webhook.
	got := make(chan string, 4)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.Method + " " + r.URL.Path + " " + r.Header.Get("X-A") + " " + string(b)
	}))
	t.Cleanup(target.Close)

	wh, err := webhook.New()
	require.NoError(t, err)

	tests := []struct {
		name  string
		event vlatte.EventConfig
	}{
		{
			name:  "built in Go",
			event: webhook.Options().URL(target.URL+"/go").Method("post").Header("X-A", "go").Body("paid").Delay(time.Millisecond).Build("webhook"),
		},
		{
			name: "from a map, with templates",
			event: vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{
				"method": "PUT", "url_template": target.URL + "/{{ .Request.Method }}",
				"header_template": map[string]any{"X-A": "{{ index .Request.URLPathSegments 0 }}"},
				"multipart_template": map[string]any{
					"type": "mixed", "boundary": "b", "parts": []any{map[string]any{"body": "{{ .Request.Method }}"}},
				},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			send := func(b vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) (string, map[string]any) {
				m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
				m.MustStart()
				defer m.Close()

				scope := m.MustMock(b)

				res, err := http.Get(m.URL("/orders"))
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusNoContent, res.StatusCode)

				m.WaitEvents()

				desc, ok := scope.Mock("orders").Describe().Props.(map[string]any)
				require.True(t, ok)

				return <-got, desc
			}

			sent, desc := send(mockhttp.Getf("/orders").ID("orders").OnCompleteEvent(tc.event).Reply(mockhttp.NoContent()))
			require.Contains(t, desc, "on_complete")

			doc, err := json.Marshal(desc)
			require.NoError(t, err)

			again, _ := send(mockhttp.FromBytes(doc, "json"))
			require.Equal(t, sent, again)
		})
	}
}
