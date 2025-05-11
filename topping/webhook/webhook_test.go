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
	"github.com/caffeine-projects/vlatte/drink/xhttp"
	. "github.com/caffeine-projects/vlatte/drink/xhttp/httpval"
	. "github.com/caffeine-projects/vlatte/filter"
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
	target := xhttp.New(xhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	testCases := []struct {
		name          string
		targetMock    *xhttp.HTTPMockBuilder
		webhookDef    vlatte.PostActionDefinition
		options       []webhook.Option
		expectedCalls int64
	}{
		{"basic with default method",
			xhttp.Getf("/third_party/hook").
				Reply(xhttp.OK()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Build(name),
			nil,
			1,
		},
		{"complex",
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
		{"no body",
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
		{"bad request from target",
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
		{"transform",
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
				webhook.WithTransform(func(_ context.Context, input vlatte.PostActionInput, args *webhook.Args) error {
					args.URL += "/transformed"
					args.Method = http.MethodPut
					args.Body = "hello world"
					return nil
				})},
			1,
		},
		{"transform with error",
			xhttp.Getf("/third_party/hook/transformed").
				Reply(xhttp.BadRequest()),
			webhook.Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodGet).
				Build(name),
			[]webhook.Option{
				webhook.WithTransform(func(_ context.Context, input vlatte.PostActionInput, args *webhook.Args) error {
					return errors.New("boom")
				})},
			0,
		},
		{"malformed url",
			xhttp.Postf("/third_party/hook").
				Reply(xhttp.OK()),
			webhook.Options().
				URL(" -   " + string(rune(0x7f))).
				Method(http.MethodPost).
				Build(name),
			nil,
			0,
		},
		{"unable to build http request",
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

			m := xhttp.New(xhttp.Setup().Silent().PostAction(name, wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustBrew(tc.targetMock)

			m.MustBrew(xhttp.Getf("/test").
				PostAction(tc.webhookDef).
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
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsClientCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	whSrv := xhttp.New(xhttp.Setup().Silent().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	whSrv.MustStartTLS()
	defer whSrv.Close()

	whSrv.MustBrew(xhttp.Postf("/third_party/hook/tls").
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

	m := xhttp.New(xhttp.Setup().
		Silent().
		TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile).
		PostAction(name, wh))
	m.MustStartTLS()
	defer m.Close()

	m.MustBrew(xhttp.Putf("/test").
		PostAction(webhook.Options().
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
	target := xhttp.New()
	target.MustStart()
	defer target.Close()

	target.MustBrew(xhttp.Getf("/hook/fault").
		Delay(vlatte.Latency(1 * time.Minute)).
		Reply(xhttp.OK().PlainText("hello")))

	wh, err := webhook.New()
	require.NoError(t, err)
	m := xhttp.New(xhttp.Setup().PostAction(name, wh))
	m.MustStart()
	defer m.Close()

	m.MustBrew(xhttp.Getf("/test").
		PostAction(webhook.Options().
			URL(target.URL("/hook/fault")).
			Method(http.MethodGet).
			Build(name)).
		Reply(xhttp.NoContent()))

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

	whSrv := xhttp.
		New(xhttp.Setup().HTTPClient(func() *http.Client {
			return client
		}))
	whSrv.MustStart()
	defer whSrv.Close()

	whSrv.MustBrew(xhttp.Getf("/hook").Reply(xhttp.OK()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := xhttp.New(xhttp.Setup().PostAction(name, wh))
	m.MustStart()
	defer m.Close()

	for i, tc := range testCases {
		tc := tc
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			m.MustBrew(xhttp.Getf("/test").
				PostAction(vlatte.PostActionDefinition{Name: name, Parameters: tc}).
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
	t.Skip("skipped until templating is fixed")

	name := "webhook"
	target := xhttp.New(xhttp.Setup())
	target.MustStart()
	defer target.Close()

	target.MustBrew(xhttp.Postf("/fs/hook").
		Headerf("hello", "world").
		Headerf("dev", "ok").
		ContentType(MIMEApplicationJSON).
		Body(Eq(`{"task": "done"}`)).
		Reply(xhttp.NoContent()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := xhttp.New(xhttp.Setup().PostAction(name, wh))
	m.SetData(map[string]any{"webhook_target": target.URL("/fs/hook")})
	m.MustStart()
	defer m.Close()

	m.MustBrew(xhttp.FromFile("testdata/1_webhook_complete_setup.yaml"))

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

			m := xhttp.New(xhttp.Setup().PostAction(name, wh))
			defer m.Close()

			mock, err := m.Brew(xhttp.FromFile(tc.filename))
			require.Error(t, err)
			require.Nil(t, mock)
		})
	}
}
