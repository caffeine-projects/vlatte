package webhook_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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
			"basic with default method",
			mockhttp.Getf("/third_party/hook").
				Reply(mockhttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Build(name),
			nil,
			1,
		},
		{
			"complex",
			mockhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body(match.Eq("hi")).
				Reply(mockhttp.OK().PlainText("bye")),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body("hi").
				Build(name),
			nil,
			1,
		},
		{
			"no body",
			mockhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Reply(mockhttp.OK().PlainText("bye")),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{
			"bad request from target",
			mockhttp.Postf("/third_party/hook").
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Reply(mockhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{
			"transform",
			mockhttp.Putf("/third_party/hook/transformed").
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body(match.Eq("hello world")).
				Reply(mockhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			[]webhook.Option{
				webhook.WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *webhook.Args) error {
					webhookArgs.URL += "/transformed"
					webhookArgs.Method = http.MethodPut
					webhookArgs.Body = "hello world"
					return nil
				}),
			},
			1,
		},
		{
			"transform with error",
			mockhttp.Getf("/third_party/hook/transformed").
				Reply(mockhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodGet).
				Build(name),
			[]webhook.Option{
				webhook.WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *webhook.Args) error {
					return errors.New("boom")
				}),
			},
			0,
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
	require.True(t, whSrv.AssertNumberOfCalls(t, 1))
	require.True(t, m.AssertNumberOfCalls(t, 1))
}

func TestWebHook_FaultTarget(t *testing.T) {
	name := "webhook"
	target := mockhttp.MustNew()
	target.MustStart()
	defer target.Close()

	target.MustMock(mockhttp.Getf("/hook/fault").
		Delay(1 * time.Minute).
		Reply(mockhttp.OK().PlainText("hello")))

	// The target never answers in time: the webhook call fails, but the mocked response must still be served.
	wh, err := webhook.New(webhook.WithRoundTripper(&http.Transport{ResponseHeaderTimeout: 100 * time.Millisecond}))
	require.NoError(t, err)
	m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(webhook.Options().
			URL(target.URL("/hook/fault")).
			Method(http.MethodGet).
			Build(name)).
		Reply(mockhttp.NoContent()))

	client := &http.Client{}
	ctxReq, cancelReq := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelReq()

	req, _ := http.NewRequestWithContext(ctxReq, http.MethodGet, m.URL("/test"), nil)
	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
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
	name := "webhook"
	target := mockhttp.MustNew()
	target.MustStart()
	defer target.Close()

	target.MustMock(mockhttp.Getf("/hook/slow").
		Delay(1 * time.Minute).
		Reply(mockhttp.OK()))

	wh, err := webhook.New(webhook.WithTimeout(100 * time.Millisecond))
	require.NoError(t, err)
	m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(webhook.Options().
			URL(target.URL("/hook/slow")).
			Method(http.MethodGet).
			Build(name)).
		Reply(mockhttp.NoContent()))

	// Without the webhook timeout, the slow target would hold the mocked response past the client timeout.
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
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
			require.True(t, target.AssertNumberOfCalls(t, 1))
		})
	}
}

// onComplete is a minimal event to call WebHook.Run directly.
type onComplete struct {
	params vlatte.Parameters
	mock   vlatte.Mock
	kit    vlatte.Kit
}

func (e onComplete) Kind() vlatte.Kind             { return mockhttp.Kind }
func (e onComplete) Kit() vlatte.Kit               { return e.kit }
func (e onComplete) Parameters() vlatte.Parameters { return e.params }
func (e onComplete) Mock() vlatte.Mock             { return e.mock }

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
