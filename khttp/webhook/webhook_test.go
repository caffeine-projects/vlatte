package webhook

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

	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/khttp"
	. "github.com/caffeine-projects/vlatte/khttp/httpval"
	. "github.com/caffeine-projects/vlatte/matcher"
)

const (
	_tlsCertFile       = "../test/testdata/cert/cert.pem"
	_tlsKeyFile        = "../test/testdata/cert/key.pem"
	_tlsClientCertFile = "../test/testdata/cert/cert_client.pem"
)

func TestWebHook_Run(t *testing.T) {
	key := "test_key"
	target := khttp.NewAPI().CloseWithT(t)
	target.MustStart()

	m := khttp.NewAPI(khttp.Setup().PostAction(Name, New())).CloseWithT(t)
	m.MustStart()

	testCases := []struct {
		name          string
		targetMock    *khttp.HTTPMockBuilder
		webhookDef    *khttp.PostActionDef
		expectedCalls int64
	}{
		{"basic with default method",
			khttp.Getf("/third_party/hook").
				Reply(khttp.OK()),
			Setup().
				URL(target.URL("/third_party/hook")).
				Build(),
			1,
		},
		{"complex",
			khttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hi")).
				Reply(khttp.OK().PlainText("bye")),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(HeaderContentType, MIMETextPlain).
				Body("hi").
				Build(),
			1,
		},
		{
			"no body",
			khttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(khttp.OK().PlainText("bye")),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header("X-Key", key).
				Header(HeaderContentType, MIMETextPlain).
				Build(),
			1,
		},
		{
			"bad request from target",
			khttp.Postf("/third_party/hook").
				Headerf(HeaderContentType, MIMETextPlain).
				Reply(khttp.BadRequest()),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(HeaderContentType, MIMETextPlain).
				Build(),
			1,
		},
		{
			"transform",
			khttp.Putf("/third_party/hook/transformed").
				Headerf(HeaderContentType, MIMETextPlain).
				Body(Eq("hello world")).
				Reply(khttp.BadRequest()),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Transform(func(input *khttp.PostActionInput, args *Args) error {
					args.URL += "/transformed"
					args.Method = http.MethodPut
					args.Body = "hello world"
					return nil
				}).
				Header(HeaderContentType, MIMETextPlain).
				Build(),
			1,
		},
		{
			"transform with error",
			khttp.Getf("/third_party/hook/transformed").
				Reply(khttp.BadRequest()),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodGet).
				Transform(func(input *khttp.PostActionInput, args *Args) error {
					return errors.New("boom")
				}).
				Build(),
			0,
		},
		{
			"malformed url",
			khttp.Postf("/third_party/hook").
				Reply(khttp.OK()),
			Setup().
				URL(" -   " + string(rune(0x7f))).
				Method(http.MethodPost).
				Build(),
			0,
		},
		{
			"unable to build http request",
			khttp.Postf("/third_party/hook").Reply(khttp.OK()),
			Setup().
				URL(target.URL("/third_party/hook")).
				Method("ghjk%&^*()").
				Build(),
			0,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer target.Clean()
			defer m.Clean()

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
	key := "test_key"
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsClientCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	target := khttp.NewAPI(khttp.Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile)).CloseWithT(t)
	target.MustStartTLS()
	target.MustMock(khttp.Postf("/third_party/hook/tls").
		Headerf("X-Key", "%s", key).
		Headerf(HeaderContentType, MIMETextPlainCharsetUTF8).
		Reply(khttp.OK().PlainText("hello")))

	m := khttp.NewAPI(khttp.Setup().
		TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile).
		PostAction(Name, New())).
		CloseWithT(t)
	m.MustStartTLS()
	m.MustMock(khttp.Putf("/test").
		PostAction(Setup().
			URL(target.URL("/third_party/hook/tls")).
			Method(http.MethodPost).
			Header("X-Key", key).
			Header(HeaderContentType, MIMETextPlainCharsetUTF8).
			SSLVerify(true).
			Body("hi").
			Build()).
		Reply(khttp.NoContent()))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
			}},
	}
	req, _ := http.NewRequest(http.MethodPut, m.URL("/test"), nil)
	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.True(t, target.AssertNumberOfCalls(t, 1))
	require.True(t, m.AssertNumberOfCalls(t, 1))
}

func TestWebHook_FaultTarget(t *testing.T) {
	target := khttp.NewAPI().CloseWithT(t)
	target.MustStart()
	target.MustMock(khttp.Getf("/hook/fault").
		Delay(kcore.Latency(1 * time.Minute)).
		Reply(khttp.OK().PlainText("hello")))

	m := khttp.NewAPI(khttp.Setup().PostAction(Name, New())).CloseWithT(t)
	m.MustStart()
	m.MustMock(khttp.Getf("/test").
		PostAction(Setup().
			URL(target.URL("/hook/fault")).
			Method(http.MethodGet).
			Build()).
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
	client := &http.Client{}
	testCases := []any{make(chan struct{}), nil}

	target := khttp.
		NewAPI(khttp.Setup().HTTPClient(func() *http.Client {
			return client
		})).
		CloseWithT(t)
	target.MustStart()
	target.MustMock(khttp.Getf("/hook").Reply(khttp.OK()))

	m := khttp.NewAPI(khttp.Setup().PostAction(Name, New())).CloseWithT(t)
	m.MustStart()

	for i, tc := range testCases {
		tc := tc
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			m.MustMock(khttp.Getf("/test").
				PostAction(&khttp.PostActionDef{Name: Name, RawParameters: tc}).
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
	target := khttp.NewAPI(khttp.Setup()).CloseWithT(t)
	target.MustStart()
	target.MustMock(khttp.Postf("/fs/hook").
		Headerf("hello", "world").
		Headerf("dev", "ok").
		ContentType(MIMEApplicationJSON).
		Body(Eq(`{"task": "done"}`)).
		Reply(khttp.NoContent()))

	m := khttp.NewAPI(khttp.Setup().PostAction(Name, New())).CloseWithT(t)
	m.SetData(map[string]any{"webhook_target": target.URL("/fs/hook")})
	m.MustStart()
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
			m := khttp.NewAPI(khttp.Setup().PostAction(Name, New())).CloseWithT(t)
			_, err := m.Mock(khttp.FromFile(tc.filename))

			require.Error(t, err)
		})
	}
}
