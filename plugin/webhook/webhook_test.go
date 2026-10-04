package webhook_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	. "github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	. "github.com/caffeine-projects/vlatte/flavor/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/plugin/webhook"
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
	target := xhttp.MustNew(xhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	testCases := []struct {
		name          string
		targetMock    *xhttp.HTTPMockBuilder
		webhookDef    vlatte.EventConfig
		options       []webhook.Option
		expectedCalls int64
	}{
		{
			"basic with default method",
			xhttp.Getf("/third_party/hook").
				Reply(xhttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Build(name),
			nil,
			1,
		},
		{
			"complex",
			xhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hi")).
				Reply(xhttp.OK().PlainText("bye")),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(HeaderContentType, MIMETextPlain).
				Body("hi").
				Build(name),
			nil,
			1,
		},
		{
			"no body",
			xhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(xhttp.OK().PlainText("bye")),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(HeaderContentType, MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{
			"bad request from target",
			xhttp.Postf("/third_party/hook").
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(xhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(HeaderContentType, MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{
			"transform",
			xhttp.Putf("/third_party/hook/transformed").
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hello world")).
				Reply(xhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(HeaderContentType, MIMETextPlain).
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
			xhttp.Getf("/third_party/hook/transformed").
				Reply(xhttp.BadRequest()),
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
		{
			"malformed url",
			xhttp.Postf("/third_party/hook").
				Reply(xhttp.OK()),
			webhook.Options().
				URL(" -   " + string(rune(0x7f))).
				Method(http.MethodPost).
				Build(name),
			nil,
			0,
		},
		{
			"unable to build http request",
			xhttp.Postf("/third_party/hook").Reply(xhttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method("ghjk%&^*()").
				Build(name),
			nil,
			0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New(tc.options...)
			require.NoError(t, err)

			m := xhttp.MustNew(xhttp.Setup().Silent().OnCompleteListener(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustMock(tc.targetMock)

			m.MustMock(xhttp.Getf("/test").
				OnCompleteEvent(tc.webhookDef).
				Reply(xhttp.NoContent()))

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

	whSrv := xhttp.MustNew(xhttp.Setup().Silent().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	whSrv.MustStartTLS()
	defer whSrv.Close()

	whSrv.MustMock(xhttp.Postf("/third_party/hook/tls").
		Headerf("X-Key", "%s", key).
		Headerf(HeaderContentType, MIMETextPlainCharsetUTF8).
		Reply(xhttp.OK().PlainText("hello")))

	wh, err := webhook.New(webhook.WithRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
		},
	}))
	require.NoError(t, err)

	m := xhttp.MustNew(xhttp.Setup().
		Silent().
		TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile).
		OnCompleteListener(name, wh))
	m.MustStartTLS()
	defer m.Close()

	m.MustMock(xhttp.Putf("/test").
		OnCompleteEvent(webhook.Options().
			URL(whSrv.URL("/third_party/hook/tls")).
			Method(http.MethodPost).
			Header("X-Key", key).
			Header(HeaderContentType, MIMETextPlainCharsetUTF8).
			Body("hi").
			Build(name)).
		Reply(xhttp.NoContent()))

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
	target := xhttp.MustNew()
	target.MustStart()
	defer target.Close()

	target.MustMock(xhttp.Getf("/hook/fault").
		Delay(vlatte.Latency(1 * time.Minute)).
		Reply(xhttp.OK().PlainText("hello")))

	// The target never answers in time: the webhook call fails, but the mocked response must still be served.
	wh, err := webhook.New(webhook.WithRoundTripper(&http.Transport{ResponseHeaderTimeout: 100 * time.Millisecond}))
	require.NoError(t, err)
	m := xhttp.MustNew(xhttp.Setup().OnCompleteListener(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(xhttp.Getf("/test").
		OnCompleteEvent(webhook.Options().
			URL(target.URL("/hook/fault")).
			Method(http.MethodGet).
			Build(name)).
		Reply(xhttp.NoContent()))

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
	name := "webhook"
	client := &http.Client{}
	testCases := []any{make(chan struct{}), nil}

	whSrv := xhttp.
		MustNew(xhttp.Setup().HTTPClient(func() *http.Client {
			return client
		}))
	whSrv.MustStart()
	defer whSrv.Close()

	whSrv.MustMock(xhttp.Getf("/hook").Reply(xhttp.OK()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := xhttp.MustNew(xhttp.Setup().OnCompleteListener(name, wh))
	m.MustStart()
	defer m.Close()

	for i, tc := range testCases {
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			m.MustMock(xhttp.Getf("/test").
				OnCompleteEvent(vlatte.EventConfig{Name: name, Parameters: tc}).
				Reply(xhttp.NoContent()))

			res, err := client.Get(m.URL("/test"))

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			m.Clean(t.Context())
		})
	}
}

func TestWebHook_FileSetup(t *testing.T) {
	name := "webhook"
	target := xhttp.MustNew(xhttp.Setup())
	target.MustStart()
	defer target.Close()

	target.MustMock(xhttp.Postf("/fs/hook").
		Headerf("hello", "world").
		Headerf("dev", "ok").
		ContentType(MIMEApplicationJSON).
		Body(Eq(`{"task": "done"}`)).
		Reply(xhttp.NoContent()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := xhttp.MustNew(xhttp.Setup().OnCompleteListener(name, wh))
	m.SetData(map[string]any{"webhook_target": target.URL("/fs/hook")})
	m.MustStart()
	defer m.Close()

	m.MustMock(xhttp.FromFile("testdata/1_webhook_complete_setup.yaml"))

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

			m := xhttp.MustNew(xhttp.Setup().OnCompleteListener(name, wh))
			defer m.Close()

			mock, err := m.Mock(xhttp.FromFile(tc.filename))
			// The error must come from the on_complete definition itself, not from on_complete being unknown.
			require.ErrorContains(t, err, "/on_complete")
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
	target := xhttp.MustNew()
	target.MustStart()
	defer target.Close()

	target.MustMock(xhttp.Getf("/hook/slow").
		Delay(vlatte.Latency(1 * time.Minute)).
		Reply(xhttp.OK()))

	wh, err := webhook.New(webhook.WithTimeout(100 * time.Millisecond))
	require.NoError(t, err)
	m := xhttp.MustNew(xhttp.Setup().OnCompleteListener(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(xhttp.Getf("/test").
		OnCompleteEvent(webhook.Options().
			URL(target.URL("/hook/slow")).
			Method(http.MethodGet).
			Build(name)).
		Reply(xhttp.NoContent()))

	// Without the webhook timeout, the slow target would hold the mocked response past the client timeout.
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestWebHook_Templates(t *testing.T) {
	name := "webhook"
	target := xhttp.MustNew(xhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	// Templates render with the request that completed the mock: PUT /orders/42.
	testCases := []struct {
		name       string
		targetMock *xhttp.HTTPMockBuilder
		parameters map[string]any
	}{
		{
			"url_template",
			xhttp.Postf("/hooks/42").Reply(xhttp.OK()),
			map[string]any{"method": "POST", "url_template": target.URL() + "/hooks/{{ index .Request.URLPathSegments 1 }}"},
		},
		{
			"header_template",
			xhttp.Postf("/hooks").Headerf("X-Method", "PUT").Reply(xhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "header_template": map[string]any{"X-Method": "{{ .Request.Method }}"}},
		},
		{
			"body_template",
			xhttp.Postf("/hooks").Body(Eq("order 42")).Reply(xhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "body_template": "order {{ index .Request.URLPathSegments 1 }}"},
		},
		{
			"body_file",
			xhttp.Postf("/hooks").Body(Eq("from a file\n")).Reply(xhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "body_file": "testdata/hook_body.txt"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New()
			require.NoError(t, err)

			m := xhttp.MustNew(xhttp.Setup().Silent().OnCompleteListener(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustMock(tc.targetMock)
			m.MustMock(xhttp.Putf("/orders/42").
				OnCompleteEvent(vlatte.EventConfig{Name: name, Parameters: tc.parameters}).
				Reply(xhttp.NoContent()))

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
type onComplete struct{ params vlatte.Parameters }

func (e onComplete) Kind() vlatte.Kind             { return vlatte.KindHTTP }
func (e onComplete) Kit() vlatte.Kit               { return nil }
func (e onComplete) Parameters() vlatte.Parameters { return e.params }
func (e onComplete) Mock() vlatte.Mock             { return nil }

func TestWebHook_TemplateErrors(t *testing.T) {
	wh, err := webhook.New()
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url_template": "http://localhost/{{ .Nope"}})
	require.ErrorIs(t, err, webhook.ErrRenderingTemplate)
}
