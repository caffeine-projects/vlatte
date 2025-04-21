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

	khttp "github.com/caffeine-projects/vlatte/bean/xhttp"
	. "github.com/caffeine-projects/vlatte/bean/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/kcore"
	. "github.com/caffeine-projects/vlatte/matcher"
	"github.com/caffeine-projects/vlatte/topping/webhook"
)

const (
	_tlsCertFile       = "./testdata/cert/cert.pem"
	_tlsKeyFile        = "./testdata/cert/key.pem"
	_tlsClientCertFile = "./testdata/cert/cert_client.pem"
)

func TestWebHook_Run(t *testing.T) {
	name := "webhook"
	key := "test_key"
	target := khttp.NewAPI()
	target.MustStart()
	defer target.Close()

	testCases := []struct {
		name          string
		targetMock    *khttp.HTTPMockBuilder
		webhookDef    kcore.PostActionDefinition
		options       []webhook.Option
		expectedCalls int64
	}{
		{"basic with default method",
			khttp.Getf("/third_party/hook").
				Reply(khttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Build(name),
			nil,
			1,
		},
		{"complex",
			khttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hi")).
				Reply(khttp.OK().PlainText("bye")),
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
		{"no body",
			khttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(khttp.OK().PlainText("bye")),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(HeaderContentType, MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{"bad request from target",
			khttp.Postf("/third_party/hook").
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(khttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(HeaderContentType, MIMETextPlain).
				Build(name),
			nil,
			1,
		},
		{"transform",
			khttp.Putf("/third_party/hook/transformed").
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hello world")).
				Reply(khttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(HeaderContentType, MIMETextPlain).
				Build(name),
			[]webhook.Option{
				webhook.WithTransform(func(_ context.Context, input kcore.PostActionInput, args *webhook.Args) error {
					args.URL += "/transformed"
					args.Method = http.MethodPut
					args.Body = "hello world"
					return nil
				})},
			1,
		},
		{"transform with error",
			khttp.Getf("/third_party/hook/transformed").
				Reply(khttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodGet).
				Build(name),
			[]webhook.Option{
				webhook.WithTransform(func(_ context.Context, input kcore.PostActionInput, args *webhook.Args) error {
					return errors.New("boom")
				})},
			0,
		},
		{"malformed url",
			khttp.Postf("/third_party/hook").
				Reply(khttp.OK()),
			webhook.Options().
				URL(" -   " + string(rune(0x7f))).
				Method(http.MethodPost).
				Build(name),
			nil,
			0,
		},
		{"unable to build http request",
			khttp.Postf("/third_party/hook").Reply(khttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method("ghjk%&^*()").
				Build(name),
			nil,
			0,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New(tc.options...)
			require.NoError(t, err)

			m := khttp.NewAPI(khttp.Setup().PostAction(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean()

			target.MustMock(tc.targetMock)

			m.MustMock(khttp.Getf("/test").
				PostAction(tc.webhookDef).
				Reply(khttp.NoContent()))

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
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsClientCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	whSrv := khttp.NewAPI(khttp.Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	whSrv.MustStartTLS()
	defer whSrv.Close()

	whSrv.MustMock(khttp.Postf("/third_party/hook/tls").
		Headerf("X-Key", "%s", key).
		Headerf(HeaderContentType, MIMETextPlainCharsetUTF8).
		Reply(khttp.OK().PlainText("hello")))

	wh, err := webhook.New(webhook.WithRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
		},
	}))
	require.NoError(t, err)

	m := khttp.NewAPI(khttp.Setup().
		TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile).
		PostAction(name, wh))
	m.MustStartTLS()
	defer m.Close()

	m.MustMock(khttp.Putf("/test").
		PostAction(webhook.Options().
			URL(whSrv.URL("/third_party/hook/tls")).
			Method(http.MethodPost).
			Header("X-Key", key).
			Header(HeaderContentType, MIMETextPlainCharsetUTF8).
			Body("hi").
			Build(name)).
		Reply(khttp.NoContent()))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
			}},
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
	target := khttp.NewAPI()
	target.MustStart()
	defer target.Close()

	target.MustMock(khttp.Getf("/hook/fault").
		Delay(kcore.Latency(1 * time.Minute)).
		Reply(khttp.OK().PlainText("hello")))

	wh, err := webhook.New()
	require.NoError(t, err)
	m := khttp.NewAPI(khttp.Setup().PostAction(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(khttp.Getf("/test").
		PostAction(webhook.Options().
			URL(target.URL("/hook/fault")).
			Method(http.MethodGet).
			Build(name)).
		Reply(khttp.NoContent()))

	client := &http.Client{}
	timeout := 2 * time.Second
	exit := make(chan struct{}, 1)
	ti := time.AfterFunc(timeout, func() { exit <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

	defer cancel()
	defer ti.Stop()

	go func() {
		for {
			select {
			case <-exit:
				return
			case <-ctx.Done():
				target.Close()
				return
			}
		}
	}()

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

	whSrv := khttp.
		NewAPI(khttp.Setup().HTTPClient(func() *http.Client {
			return client
		}))
	whSrv.MustStart()
	defer whSrv.Close()

	whSrv.MustMock(khttp.Getf("/hook").Reply(khttp.OK()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := khttp.NewAPI(khttp.Setup().PostAction(name, wh))
	m.MustStart()
	defer m.Close()

	for i, tc := range testCases {
		tc := tc
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			m.MustMock(khttp.Getf("/test").
				PostAction(kcore.PostActionDefinition{Name: name, Parameters: tc}).
				Reply(khttp.NoContent()))

			res, err := client.Get(m.URL("/test"))

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			m.Clean()
		})
	}
}

func TestWebHook_FileSetup(t *testing.T) {
	name := "webhook"
	target := khttp.NewAPI(khttp.Setup())
	target.MustStart()
	defer target.Close()

	target.MustMock(khttp.Postf("/fs/hook").
		Headerf("hello", "world").
		Headerf("dev", "ok").
		ContentType(MIMEApplicationJSON).
		Body(Eq(`{"task": "done"}`)).
		Reply(khttp.NoContent()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := khttp.NewAPI(khttp.Setup().PostAction(name, wh))
	m.SetData(map[string]any{"webhook_target": target.URL("/fs/hook")})
	m.MustStart()
	defer m.Close()

	m.MustMock(khttp.FromFile("testdata/1_webhook_complete_setup.yaml"))

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
		{"post actions must be an an array", "testdata/2_webhook_object_not_array.yaml"},
		{"item is not an object", "testdata/3_webhook_invalid_item_type.yaml"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wh, err := webhook.New()
			require.NoError(t, err)

			m := khttp.NewAPI(khttp.Setup().PostAction(name, wh))
			defer m.Close()

			mock, err := m.Mock(khttp.FromFile(tc.filename))
			require.Error(t, err)
			require.Nil(t, mock)
		})
	}
}
