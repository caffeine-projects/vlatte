package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
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
		options       []Option
		expectedCalls int64
	}{
		{
			name:          "basic with default method",
			targetMock:    mockhttp.Getf("/third_party/hook").Reply(mockhttp.OK()),
			webhookDef:    Options().URL(target.URL("/third_party/hook")).Build(name),
			expectedCalls: 1,
		},
		{
			name: "complex",
			targetMock: mockhttp.Postf("/third_party/hook").
				Headerf("X-Key", "%s", key).
				Headerf(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Body(match.Eq("hi")).
				Reply(mockhttp.OK().PlainText("bye")),
			webhookDef: Options().
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
			webhookDef: Options().
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
			webhookDef: Options().
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
			webhookDef: Options().
				URL(target.URL("/third_party/hook")).
				Method(http.MethodPost).
				Header(mockhttp.HeaderContentType, mockhttp.MIMETextPlain).
				Build(name),
			options: []Option{
				WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *Args) error {
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
			webhookDef: Options().URL(target.URL("/third_party/hook")).Method(http.MethodGet).Build(name),
			options: []Option{
				WithTransform(func(_ context.Context, args vlatte.OnComplete, webhookArgs *Args) error {
					return errors.New("boom")
				}),
			},
			expectedCalls: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := New(tc.options...)
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

	wh, err := New(WithTransport(&http.Transport{
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
		OnCompleteEvent(Options().
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

func TestWebHook_TheTargetHasTheWebhookWhenTheResponseComes(t *testing.T) {
	// A test checks a webhook's target right after the response, with no wait of its own: the response waits for its
	// webhooks, even to a slow target.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)

	target.MustMock(mockhttp.Postf("/hook").Delay(vlatte.Latency(100 * time.Millisecond)).Reply(mockhttp.NoContent()))

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(Options().URL(target.URL("/hook")).Method(http.MethodPost).Build("webhook")).
		Reply(mockhttp.Accepted()))

	res, err := http.Get(m.URL("/test"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	require.True(t, target.AssertNumberOfCalls(t, 1))
}

func TestWebHook_SendsEveryValueOfAHeader(t *testing.T) {
	// A webhook sent one value per header, while replies send several, as a target may need a header given twice:
	// a list of values sends each of them, in order, from a mock file and from Go.
	got := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got <- r.Header }))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	tests := []struct {
		name string
		mock vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
	}{
		{
			name: "from a mock file",
			mock: mockhttp.FromBytes([]byte(`
request: {method: GET, path: /orders}
response: {status: 204}
on_complete:
  - name: webhook
    parameters:
      url: `+strconv.Quote(target.URL)+`
      header: {X-Tag: [a, b]}
      header_template: {X-Seen: ['{{ .Request.Method }}', two]}
`), "yaml"),
		},
		{
			name: "from Go",
			mock: mockhttp.Getf("/orders").
				OnCompleteEvent(Options().URL(target.URL).
					Header("X-Tag", "a").
					Header("X-Tag", "b").
					HeaderTemplate("X-Seen", "{{ .Request.Method }}").
					HeaderTemplate("X-Seen", "two").
					Build("webhook")).
				Reply(mockhttp.NoContent()),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(tc.mock)

			res, err := http.Get(m.URL("/orders"))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			header := <-got
			require.Equal(t, []string{"a", "b"}, header.Values("X-Tag"))
			require.Equal(t, []string{"GET", "two"}, header.Values("X-Seen"))
		})
	}
}

func TestWebHook_AfterTheResponseTheClientHasItsAnswerFirst(t *testing.T) {
	// An app that needs its callback once it has its answer, as a payment API's, never got it so: the response waited
	// for its webhooks. With after_response, the client has its answer while the target still holds the webhook, and
	// the target then gets the whole webhook.
	arrived, release := make(chan struct{}), make(chan struct{})
	answered := make(chan bool, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		answered <- r.Context().Err() == nil
	}))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromBytes([]byte(`
request: {method: POST, path: /orders}
response: {status: 202}
on_complete:
  - name: webhook
    after_response: true
    parameters: {method: POST, url: `+strconv.Quote(target.URL)+`}
`), "yaml"))

	res, err := http.Post(m.URL("/orders"), "text/plain", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	<-arrived
	close(release)
	require.True(t, <-answered, "the webhook waited for its target past the response, and wasn't cut short")
}

func TestWebHook_InvalidArgs(t *testing.T) {
	// Parameters that are neither *Args nor a map fail the mock when it is built, not each request after it.
	name := "webhook"

	wh, err := New()
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
		{"nil *Args", (*Args)(nil), "webhook: arguments must not be nil"},
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

	wh, err := New()
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
			wh, err := New()
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

func TestWebHook_AStreamedReplyEndsAfterItsWebhooks(t *testing.T) {
	// A streamed reply sends its status and headers before its webhooks are done, so the call returns early, but its
	// body ends after them: a test that reads the body to its end checks the target right after.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)

	target.MustMock(mockhttp.Postf("/hook").Delay(vlatte.Latency(100 * time.Millisecond)).Reply(mockhttp.NoContent()))

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/stream").
		OnCompleteEvent(Options().URL(target.URL("/hook")).Method(http.MethodPost).Build("webhook")).
		Reply(mockhttp.OK().Multipart(mockhttp.MultipartMixed(
			mockhttp.NewPart().ContentType("text/plain").BodyText("frame 1"),
			mockhttp.NewPart().ContentType("text/plain").BodyText("frame 2").Delay(50*time.Millisecond),
		))))

	res, err := http.Get(m.URL("/stream"))
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Contains(t, string(body), "frame 2")

	require.True(t, target.AssertNumberOfCalls(t, 1))
}

func TestWebHook_Timeout(t *testing.T) {
	// The timeout bounds a send: a target that never answers fails it after exactly the timeout.
	synctest.Test(t, func(t *testing.T) {
		wh, err := New(WithTransport(silent{}), WithTimeout(time.Minute))
		require.NoError(t, err)

		start := time.Now()
		err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test/slow"}})

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, time.Minute, time.Since(start))
	})
}

func TestWebHook_TimeoutPrecedence(t *testing.T) {
	// A timeout is a webhook setting like any other: a webhook's own timeout parameter wins, as the most specific,
	// over WithTimeout, the timeout of every webhook its listener sends, such as the vlatte command's webhook.timeout
	// setting. Without either, a webhook waits 10s.
	testCases := []struct {
		name   string
		option time.Duration
		params vlatte.Parameters
		want   time.Duration
	}{
		{
			name: "the parameter wins over WithTimeout", option: time.Minute, want: time.Second,
			params: map[string]any{"url": "http://hooks.test/slow", "timeout": "1s"},
		},
		{
			name: "the parameter set in Go", option: time.Minute, want: 2 * time.Second,
			params: Options().URL("http://hooks.test/slow").Timeout(2 * time.Second).Build("webhook").Parameters,
		},
		{
			name: "WithTimeout without the parameter", option: time.Minute, want: time.Minute,
			params: map[string]any{"url": "http://hooks.test/slow"},
		},
		{name: "10s without either", params: map[string]any{"url": "http://hooks.test/slow"}, want: 10 * time.Second},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				options := []Option{WithTransport(silent{})}
				if tc.option > 0 {
					options = append(options, WithTimeout(tc.option))
				}

				wh, err := New(options...)
				require.NoError(t, err)

				start := time.Now()
				err = wh.Run(t.Context(), onComplete{params: tc.params})

				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, tc.want, time.Since(start))
			})
		})
	}
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
			wh, err := New()
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

func TestArgsBuilder_Templates(t *testing.T) {
	// Go users set templates with the builder, as mock files set them with url_template, header_template,
	// body_template and body_file, and they render the same: with the request that completed the mock, PUT /orders/42.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	testCases := []struct {
		name       string
		targetMock *mockhttp.Builder
		args       *ArgsBuilder
	}{
		{
			"URLTemplate",
			mockhttp.Postf("/hooks/42").Reply(mockhttp.OK()),
			Options().URLTemplate(target.URL() + "/hooks/{{ index .Request.URLPathSegments 1 }}"),
		},
		{
			"HeaderTemplate",
			mockhttp.Postf("/hooks").Headerf("X-Method", "PUT").Headerf("X-Plain", "{{ .Request.Method }}").Reply(mockhttp.OK()),
			Options().URL(target.URL("/hooks")).
				HeaderTemplate("X-Method", "{{ .Request.Method }}").
				Header("X-Plain", "{{ .Request.Method }}"),
		},
		{
			"BodyTemplate",
			mockhttp.Postf("/hooks").Body(match.Eq("order 42")).Reply(mockhttp.OK()),
			Options().URL(target.URL("/hooks")).BodyTemplate("order {{ index .Request.URLPathSegments 1 }}"),
		},
		{
			"BodyFile",
			mockhttp.Postf("/hooks").Body(match.Eq("from a file\n")).Reply(mockhttp.OK()),
			Options().URL(target.URL("/hooks")).BodyFile("testdata/hook_body.txt"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh, err := New()
			require.NoError(t, err)

			m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
			m.MustStart()
			defer m.Close()
			defer target.Clean(t.Context())

			target.MustMock(tc.targetMock)
			m.MustMock(mockhttp.Putf("/orders/42").
				OnCompleteEvent(tc.args.Method(http.MethodPost).Build("webhook")).
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

func TestArgsBuilder_TemplatesAreCheckedWhenTheMockIsBuilt(t *testing.T) {
	// A template set with the builder is compiled when the mock is built, as one from a mock file is.
	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	defer m.Close()

	for name, args := range map[string]*ArgsBuilder{
		"url_template":      Options().URLTemplate("{{ .X"),
		"header_template X": Options().URL("http://localhost").HeaderTemplate("X", "{{ .X"),
		"body_template":     Options().URL("http://localhost").BodyTemplate("{{ .X"),
	} {
		_, err := m.Mock(mockhttp.Getf("/x").OnCompleteEvent(args.Build("webhook")).Reply(mockhttp.OK()))
		require.ErrorIs(t, err, ErrRenderingTemplate, name)
		require.ErrorContains(t, err, "template: "+name+":1: unclosed action")
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

// kit is the Kit of a server without a template engine: templates render with data. Without a logger, it logs
// nothing.
type kit struct {
	data   any
	logger *zerolog.Logger
}

func (k kit) TemplateEngine() vlatte.TemplateEngine { return nil }
func (k kit) Data() any                             { return k.data }
func (k kit) Transport() http.RoundTripper          { return http.DefaultTransport }
func (k kit) Dir() string                           { return "" }

func (k kit) Logger() *zerolog.Logger {
	if k.logger == nil {
		nop := zerolog.Nop()
		return &nop
	}

	return k.logger
}

// sent is a RoundTripper that keeps the request it is given and answers 200.
type sent struct {
	req *http.Request
}

func (s *sent) RoundTrip(req *http.Request) (*http.Response, error) {
	s.req = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

func TestWebHook_TemplateErrors(t *testing.T) {
	wh, err := New()
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url_template": "http://localhost/{{ .Nope"}})
	require.ErrorIs(t, err, ErrRenderingTemplate)

	// Each value of a header renders: a broken one after a good one fails the send.
	err = wh.Run(t.Context(), onComplete{params: map[string]any{
		"url": "http://localhost", "header_template": map[string]any{"X-A": []any{"ok", "{{ .Nope"}},
	}})
	require.ErrorIs(t, err, ErrRenderingTemplate)
}

func TestWebHook_ABodyTemplateExecutionFailurePreventsTheSend(t *testing.T) {
	transport := &sent{}
	wh, err := New(WithTransport(transport))
	require.NoError(t, err)
	err = wh.Run(t.Context(), onComplete{
		params: map[string]any{"url": "http://hooks.test", "body_template": "{{ .missing }}"},
		kit:    kit{data: make(map[string]any)},
	})
	require.ErrorIs(t, err, ErrRenderingTemplate)
	require.Nil(t, transport.req)
}

func TestWebHook_RunErrorsSayWhatTheWebhookWasDoing(t *testing.T) {
	errTransform := errors.New("boom")
	wh, err := New(WithTransform(func(context.Context, vlatte.OnComplete, *Args) error { return errTransform }))
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://localhost"}})
	require.ErrorIs(t, err, errTransform)
	require.EqualError(t, err, "webhook: transforming the args: boom")

	wh, err = New()
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://localhost", "body_file": "testdata/missing.txt"}})
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorContains(t, err, "webhook: reading body_file testdata/missing.txt: ")

	wh, err = New(WithTransport(refused{}))
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test/x"}})
	require.ErrorIs(t, err, errRefused)
	require.EqualError(t, err, "webhook: GET http://hooks.test/x: sending the request: refused")
}

var errRefused = errors.New("refused")

// refused is a RoundTripper whose target refuses every request.
type refused struct{}

func (refused) RoundTrip(*http.Request) (*http.Response, error) { return nil, errRefused }

func TestWebHook_KitWithoutAnEngine(t *testing.T) {
	// A Kit without a template engine renders the event's templates with Go templates, and the Kit's data.
	rt := &sent{}
	wh, err := New(WithTransport(rt))
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
	wh, err := New(WithTransport(rt))
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
			wh, err := New(WithTransport(rt))
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
	wh, err := New(WithTransport(rt))
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

			wh, err := New(WithTransport(&answer{status: tc.status}), WithLogger(&logger))
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
	wh, err := New(WithTransport(rt))
	require.NoError(t, err)

	require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test/direct"}, kit: kit{logger: &logger}}))
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
	require.Contains(t, out.String(), "<--- WEBHOOK GET /server")
}

// carrier is a transport of the caller's: it counts the webhooks it carries, and sends them on.
type carrier struct {
	inner *http.Transport
	calls atomic.Int64
}

func newCarrier(t *testing.T) *carrier {
	t.Helper()

	inner := &http.Transport{}
	t.Cleanup(inner.CloseIdleConnections)

	return &carrier{inner: inner}
}

func (c *carrier) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.inner.RoundTrip(r)
}

func TestWebHook_ConnectionsCloseWithTheServer(t *testing.T) {
	// Webhooks sent through http.DefaultTransport, which kept their idle connections to the target for 90s after the
	// server closed, unless something closed every idle connection of the process, as an httptest.Server's Close does.
	// A server without a transport of the caller's now gives webhooks one it closes, whatever its backend.
	//
	// Two sends share one connection, as a webhook reads the answer before it ends. Closed unread, the answer was
	// drained by net/http after the send, for at most 50ms: under load, the connection was lost, and the second send
	// dialed again. The target sends the second half of its answer after those 50ms.
	var opened, open atomic.Int64

	half := strings.Repeat("a", 16<<10)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, half)
		_ = http.NewResponseController(w).Flush()

		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, half)
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			opened.Add(1)
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	target.Start()
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().BackendFactory(plainBackends{}).OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/test").
		OnCompleteEvent(Options().URL(target.URL + "/hook").Method(http.MethodPost).Build("webhook")).
		Reply(mockhttp.NoContent()))

	client := &http.Client{Transport: &http.Transport{}}
	for range 2 {
		res, err := client.Get(m.URL("/test"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusNoContent, res.StatusCode)
	}

	require.Equal(t, int64(1), opened.Load(), "the second webhook reused the connection of the first")

	m.Close()

	require.Eventually(t, func() bool { return open.Load() == 0 }, 2*time.Second, 10*time.Millisecond,
		"connections to the target left open after Close: %d", open.Load())
}

// plainBackends builds mock server backends on an http.Server, which, unlike an httptest.Server, leaves the idle
// connections of http.DefaultTransport alone when it closes.
type plainBackends struct{}

func (plainBackends) NewBackend(cfg mockhttp.BackendConfig) (mockhttp.Backend, error) {
	return &plainBackend{server: &http.Server{Handler: cfg.Handler}}, nil
}

type plainBackend struct {
	server *http.Server
	ln     net.Listener
	info   mockhttp.BackendInfo
}

func (b *plainBackend) Listen() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	b.ln, b.info.URL = ln, "http://"+ln.Addr().String()

	return nil
}

func (b *plainBackend) ListenTLS() error { return errors.New("plain backend: no TLS") }

func (b *plainBackend) Serve() error {
	go b.server.Serve(b.ln)
	return nil
}

func (b *plainBackend) Close() error                { return b.server.Close() }
func (b *plainBackend) Info() *mockhttp.BackendInfo { return &b.info }

func TestWebHook_SendsThroughTheServerTransport(t *testing.T) {
	// The server's transport carries what it sends, its webhooks included, such as through a client certificate or a
	// proxy of the caller's network. A webhook's own transport goes first. With neither, it sends all the same.
	var hooks atomic.Int64

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hooks.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	tests := []struct {
		name       string
		server     bool
		own        bool
		wantServer int64
		wantOwn    int64
	}{
		{name: "the server's", server: true, wantServer: 1},
		{name: "its own first", server: true, own: true, wantOwn: 1},
		{name: "neither"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, own := newCarrier(t), newCarrier(t)

			var options []Option
			if tt.own {
				options = append(options, WithTransport(own))
			}

			wh, err := New(options...)
			require.NoError(t, err)

			setup := mockhttp.Setup().Silent().OnCompleteListener("webhook", wh)
			if tt.server {
				setup = setup.Transport(server)
			}

			m := mockhttp.MustNew(setup)
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(mockhttp.Getf("/test").
				OnCompleteEvent(Options().URL(target.URL + "/hook").Method(http.MethodPost).Build("webhook")).
				Reply(mockhttp.NoContent()))

			before := hooks.Load()

			res, err := http.Get(m.URL("/test"))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			// The response waits for its webhooks.
			require.Equal(t, before+1, hooks.Load(), "the webhook reached its target")
			require.Equal(t, tt.wantServer, server.calls.Load())
			require.Equal(t, tt.wantOwn, own.calls.Load())
		})
	}
}

func TestWebHook_AZeroWebHookSends(t *testing.T) {
	// A WebHook built without New panicked: it had no Transform, RoundTripper or Logger, and no timeout.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Method + " " + r.URL.Path
	}))
	t.Cleanup(target.Close)

	var wh WebHook
	require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"method": "POST", "url": target.URL + "/x"}}))
	require.Equal(t, "POST /x", <-got)
}

func TestWithTimeout_RejectsANegativeTimeout(t *testing.T) {
	// A negative timeout made every send fail at once.
	_, err := New(WithTimeout(-time.Second))
	require.EqualError(t, err, "webhook: option at index 0: negative timeout -1s")
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
	// A delay holds the send, as real webhooks often come some time after what triggers them: it counts from the start
	// of the send, and the timeout from the request, after it. A context that ends during the delay sends nothing.
	t.Run("sent after the delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &clocked{}
			wh, err := New(WithTransport(rt))
			require.NoError(t, err)

			start := time.Now()
			require.NoError(t, wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://hooks.test", "delay": "1.5s"}}))

			require.Len(t, rt.at, 1)
			require.Equal(t, 1500*time.Millisecond, rt.at[0].Sub(start))
		})
	})

	t.Run("the timeout counts from the send", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			wh, err := New(WithTransport(silent{}), WithTimeout(time.Second))
			require.NoError(t, err)

			start := time.Now()
			err = wh.Run(t.Context(), onComplete{params: &Args{URL: "http://hooks.test", Delay: 2 * time.Second}})

			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, 3*time.Second, time.Since(start), "the delay, then the timeout")
		})
	})

	t.Run("a context that ends during the delay sends nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &clocked{}
			wh, err := New(WithTransport(rt))
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
	// The delay of a mock's webhook holds the webhook, and so the response, which waits for its webhooks: the target
	// has the webhook when the response comes, the delay after the request.
	got := make(chan time.Time, 1)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got <- time.Now() }))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/x").
		OnCompleteEvent(Options().URL(target.URL).Delay(200 * time.Millisecond).Build("webhook")).
		Reply(mockhttp.NoContent()))

	start := time.Now()

	res, err := http.Get(m.URL("/x"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	select {
	case sent := <-got:
		require.GreaterOrEqual(t, sent.Sub(start), 200*time.Millisecond, "the webhook came after its delay")
	default:
		t.Fatal("the response came before the webhook")
	}
}

func TestArgs_ADescriptionDecodesBack(t *testing.T) {
	// A mock's description holds its webhook's parameters as a mock file writes them, the ones set only: decoded, they
	// are the same parameters.
	tests := []struct {
		name string
		args *Args
	}{
		{
			name: "text keys",
			args: &Args{
				URL: "http://hooks.test/x", Method: "POST", Header: http.Header{"X-A": {"1"}},
				HeaderTemplate: map[string][]string{"X-B": {"{{ .Request.Method }}"}}, BodyTemplate: "{{ .Request.URL }}",
				Delay: 1500 * time.Millisecond, Timeout: 2 * time.Second,
			},
		},
		{
			name: "headers with several values",
			args: &Args{
				URL: "http://hooks.test/x", Header: http.Header{"X-A": {"1", "2"}},
				HeaderTemplate: map[string][]string{"X-B": {"{{ .Request.Method }}", "b"}},
			},
		},
		{
			name: "a url_template and a body file",
			args: &Args{URLTemplate: "http://hooks.test/{{ .Request.Method }}", BodyFile: "testdata/hook_body.txt"},
		},
		{
			name: "a body",
			args: &Args{URL: "http://hooks.test", Body: "paid"},
		},
		{
			name: "a body that isn't UTF-8",
			args: &Args{URL: "http://hooks.test", Body: "\xff\xfe\x00"},
		},
		{
			name: "a multipart body",
			args: &Args{URL: "http://hooks.test", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart().BodyText("a")).Boundary("b")},
		},
		{
			name: "a multipart body whose text is templates",
			args: &Args{
				URL:       "http://hooks.test",
				Multipart: mockhttp.MultipartForm(mockhttp.NewPart().Name("n").BodyText("{{ .X }}")).Template(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desc, ok := tc.args.Describe().Props.(map[string]any)
			require.True(t, ok)

			decoded, err := ArgsFromMap(desc)
			require.NoError(t, err)
			require.Equal(t, desc, decoded.Describe().Props, "the description decodes to parameters described the same")

			if tc.args.Multipart == nil {
				require.Equal(t, tc.args, decoded)
			}
		})
	}

	header := (&Args{Header: http.Header{"X-One": {"1"}, "X-Two": {"1", "2"}}}).Describe().Props.(map[string]any)["header"]
	require.Equal(t, map[string]any{"X-One": "1", "X-Two": []string{"1", "2"}}, header,
		"a header with one value is written as a mock file writes it: a string")

	_, templated := (&Args{Multipart: mockhttp.MultipartMixed().Template()}).Describe().Props.(map[string]any)["multipart_template"]
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

	wh, err := New()
	require.NoError(t, err)

	orders := func(event vlatte.EventConfig) vlatte.Builder[*mockhttp.Mock, *mockhttp.Server] {
		return mockhttp.Getf("/orders").ID("orders").OnCompleteEvent(event).Reply(mockhttp.NoContent())
	}

	// mockFile writes a mock whose webhook has parameters to a directory of its own, with files, which relative body
	// files open from: a description loaded back has no directory.
	mockFile := func(parameters string, files map[string]string) vlatte.Builder[*mockhttp.Mock, *mockhttp.Server] {
		dir := t.TempDir()
		for name, content := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
		}

		doc := "id: orders\nrequest: {method: GET, path: /orders}\nresponse: {status: 204}\n" +
			"on_complete:\n  - name: webhook\n    parameters: " + parameters + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "orders.yaml"), []byte(doc), 0o600))

		return mockhttp.FromFile(filepath.Join(dir, "orders.yaml"))
	}

	tests := []struct {
		name string
		mock vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
	}{
		{
			name: "built in Go",
			mock: orders(Options().URL(target.URL+"/go").Method("post").Header("X-A", "go").Body("paid").Delay(time.Millisecond).Build("webhook")),
		},
		{
			name: "built in Go, with a body that isn't UTF-8",
			mock: orders(Options().URL(target.URL + "/bytes").Method("post").Body("\xff\xfe\x00").Build("webhook")),
		},
		{
			name: "built in Go, with a part that isn't UTF-8",
			mock: orders(Options().URL(target.URL + "/part-bytes").Method("post").
				Multipart(mockhttp.MultipartMixed(mockhttp.NewPart().Body([]byte{0xff, 0xfe, 0x00})).Boundary("b")).Build("webhook")),
		},
		{
			name: "from a map, with templates",
			mock: orders(vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{
				"method": "PUT", "url_template": target.URL + "/{{ .Request.Method }}",
				"header_template": map[string]any{"X-A": "{{ index .Request.URLPathSegments 0 }}"},
				"multipart_template": map[string]any{
					"type": "mixed", "boundary": "b", "parts": []any{map[string]any{"body": "{{ .Request.Method }}"}},
				},
			}}),
		},
		{
			name: "from a mock file, with a body file",
			mock: mockFile("{method: POST, url: "+strconv.Quote(target.URL+"/file")+", body_file: hook.txt}",
				map[string]string{"hook.txt": "paid"}),
		},
		{
			name: "from a mock file, with a part's body file",
			mock: mockFile("{method: POST, url: "+strconv.Quote(target.URL+"/part")+
				", multipart: {type: mixed, boundary: b, parts: [{body_file: part.txt}]}}",
				map[string]string{"part.txt": "receipt"}),
		},
		{
			name: "from a mock file, with a part's body file named by a template",
			mock: mockFile("{method: POST, url: "+strconv.Quote(target.URL+"/template")+
				", multipart_template: {type: mixed, boundary: b, parts: [{body_file: '{{ index .Request.URLPathSegments 0 }}.txt'}]}}",
				map[string]string{"orders.txt": "receipt"}),
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

				desc, ok := scope.Mock("orders").Describe().Props.(map[string]any)
				require.True(t, ok)

				var sent string
				select {
				case sent = <-got:
				default:
					t.Fatal("the target had no webhook when the response came")
				}

				return sent, desc
			}

			sent, desc := send(tc.mock)
			require.Contains(t, desc, "on_complete")

			doc, err := json.Marshal(desc)
			require.NoError(t, err)

			again, _ := send(mockhttp.FromBytes(doc, "json"))
			require.Equal(t, sent, again)
		})
	}
}

func TestWebHook_ASavedMockStillSendsItsBodyFile(t *testing.T) {
	// The admin API saves a mock as a mock file, which names the body file of its webhook from its own directory: the
	// webhook sends the file still, once the server serves the saved mock file.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("bodies", 0o700))
	require.NoError(t, os.WriteFile("bodies/hook.json", []byte(`{"from": "a file"}`), 0o600))

	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
	}))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()).OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{"id": "hooked", "request": {"method": "GET", "path": "/hooked"}, "response": {"status": 202},
		"on_complete": [{"name": "webhook", "parameters": {"url": "` + target.URL + `", "body_file": "bodies/hook.json"}}]}`
	res, err := http.Post(m.AdminURL()+"/mocks", "application/json", strings.NewReader(doc))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, err = http.Post(m.AdminURL()+"/mocks/save", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	saved, err := os.ReadFile("testdata/vlatte/_mocks/hooked.mock.yaml")
	require.NoError(t, err)
	require.Contains(t, string(saved), "body_file: ../../../bodies/hook.json")

	res, err = http.Get(m.URL("/hooked"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	// The response waits for its webhooks.
	select {
	case body := <-got:
		require.JSONEq(t, `{"from": "a file"}`, body)
	default:
		t.Fatal("the webhook wasn't sent")
	}
}

func TestArgsFromMap(t *testing.T) {
	// Parameters decode the way mock files do; a map built in code means what its JSON means.
	args, err := ArgsFromMap(map[string]any{
		"url":             "http://a",
		"method":          "POST",
		"header":          map[string]string{"A": "1"},
		"header_template": map[string]any{"B": "{{ .X }}"},
		"body_template":   "x",
		"delay":           "1.5s",
		"timeout":         "2s",
	})

	require.NoError(t, err)
	require.Equal(t, &Args{
		URL:            "http://a",
		Method:         "POST",
		Header:         http.Header{"A": {"1"}},
		HeaderTemplate: map[string][]string{"B": {"{{ .X }}"}},
		BodyTemplate:   "x",
		Delay:          1500 * time.Millisecond,
		Timeout:        2 * time.Second,
	}, args)
}

func TestArgsFromMap_BodyBase64(t *testing.T) {
	// JSON and YAML hold text as UTF-8: a body of other bytes, such as an image, is written in standard base64, as
	// replies write theirs, and sent as those bytes. It is one body, as the others are.
	args, err := ArgsFromMap(map[string]any{"url": "http://a", "body_base64": "//4A\n"})
	require.NoError(t, err)
	require.Equal(t, &Args{URL: "http://a", Body: "\xff\xfe\x00"}, args)

	_, err = ArgsFromMap(map[string]any{"url": "http://a", "body": "x", "body_base64": "//4"})
	require.ErrorIs(t, err, ErrBadArg)
	require.EqualError(t, err, `webhook: bad argument: parameters: 2 problems
  on_complete: invalid_value: parameters.body_base64 isn't standard base64: illegal base64 data at input byte 0; encode the bytes with base64, padding included
  on_complete: conflict: parameters.body_base64 is set with parameters.body; keep one of them`)
}

func TestArgsFromMap_ReportsEveryProblem(t *testing.T) {
	// Parameters used to fall back to defaults when a value had the wrong type, and log_level ignored the int64 of
	// mock files. Now each problem is reported, as in mock files, all at once.
	_, err := ArgsFromMap(map[string]any{
		"url":          "http://a",
		"url_template": "http://b",
		"body":         "x",
		"body_file":    "y",
		"method":       5,
		"header":       map[string]any{"X": 1},
		"log_level":    -1,
	})

	require.ErrorIs(t, err, ErrBadArg)
	require.EqualError(t, err, `webhook: bad argument: parameters: 5 problems
  on_complete: conflict: parameters.body_file is set with parameters.body; keep one of them
  on_complete: invalid_value: parameters.header.X must be a string or a list of strings, not a number; write it in quotes: "1"
  on_complete: unknown_key: parameters.log_level is not a known setting; `+
		`the keys under parameters are url, url_template, method, header, header_template, body, body_base64, body_template, body_file, multipart, multipart_template, delay, timeout
  on_complete: invalid_value: parameters.method must be a string, not a number; write it in quotes: "5"
  on_complete: conflict: parameters.url_template is set with parameters.url; keep one of them`)
}

func TestArgsFromMap_ValuesJSONCantWrite(t *testing.T) {
	// A map built in code that JSON can't write isn't parameters either.
	_, err := ArgsFromMap(map[string]any{"url": make(chan int)})
	require.ErrorIs(t, err, ErrBadArg)
}

func TestWebHook_RunNamesTheMockOfBadParameters(t *testing.T) {
	// Parameters are checked when the event runs, so the problem names the mock whose on_complete holds them.
	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	mock, err := mockhttp.Getf("/x").ID("users").Reply(mockhttp.OK()).Build(m)
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://a", "log_level": -1}, mock: mock})
	require.ErrorIs(t, err, ErrBadArg)
	require.ErrorContains(t, err, "mock users: unknown_key: parameters.log_level")
}

// takes reports whether the parameter key takes v: strings, and objects of strings for headers. A multipart object
// needs a type and parts, which no drawn value has.
func takes(key string, v any) bool {
	switch key {
	case "multipart", "multipart_template":
		return false
	case "header", "header_template":
		switch m := v.(type) {
		case map[string]string:
			return true
		case map[string]any:
			for _, x := range m {
				if !headerValue(x) {
					return false
				}
			}

			return true
		case map[string][]string:
			return true
		}

		return false
	case "delay", "timeout":
		// Whole milliseconds from 0, or a Go duration, which no drawn string is.
		switch v.(type) {
		case int, int64:
			return true
		}

		return false
	case "log_level", "ssl_verify":
		return false
	default:
		_, ok := v.(string)
		return ok
	}
}

// requireHeader asserts that got, a header of Args, holds want, whichever of the header types it has.
func requireHeader(t require.TestingT, want map[string][]string, got any) {
	switch h := got.(type) {
	case http.Header:
		require.Equal(t, want, map[string][]string(h))
	default:
		require.Equal(t, want, got)
	}
}

// headerValue reports whether x is a value a header takes: a string, or a list of strings.
func headerValue(x any) bool {
	switch x := x.(type) {
	case string:
		return true
	case []any:
		for _, item := range x {
			if _, ok := item.(string); !ok {
				return false
			}
		}

		return true
	}

	return false
}

func TestPropArgsFromMap_Types(t *testing.T) {
	// W1: a parameter is taken exactly when it has its type, as in mock files: strings, and objects of strings, or of
	// lists of strings, for headers, whatever Go types code builds them with. Keys the webhook doesn't read are never taken.
	keys := []string{
		"url", "url_template", "method", "body", "body_template", "body_file", "header", "header_template",
		"multipart", "multipart_template", "delay", "timeout", "log_level", "ssl_verify",
	}
	values := []any{
		nil, true, "x", "", 5, int64(5), 1.5, float32(0.5),
		[]string{"a"},
		[]any{"a"},
		map[string]any{"a": "b"},
		map[string]string{"a": "b"},
		map[string]any{"a": 1},
		map[string]any{"a": []any{"b", "c"}},
		map[string]any{"a": []any{"b", 1}},
		map[string][]string{"a": {"b", "c"}},
		make(map[string]any),
	}

	rapid.Check(t, func(t *rapid.T) {
		key := rapid.SampledFrom(keys).Draw(t, "key")
		v := rapid.SampledFrom(values).Draw(t, "value")

		args, err := ArgsFromMap(map[string]any{key: v})

		if !takes(key, v) {
			require.ErrorIs(t, err, ErrBadArg)
			require.ErrorContains(t, err, "parameters."+key)

			return
		}

		require.NoError(t, err)

		got := map[string]any{
			"url": args.URL, "url_template": args.URLTemplate, "method": args.Method, "body": args.Body,
			"body_template": args.BodyTemplate, "body_file": args.BodyFile, "header": args.Header, "header_template": args.HeaderTemplate,
			"delay": args.Delay, "timeout": args.Timeout,
		}

		switch x := v.(type) {
		case int, int64:
			require.Equal(t, 5*time.Millisecond, got[key], "the drawn numbers are 5: milliseconds")
		case string:
			require.Equal(t, x, got[key])
		case map[string]string:
			header := make(map[string][]string, len(x))
			for name, value := range x {
				header[name] = []string{value}
			}

			requireHeader(t, header, got[key])
		case map[string][]string:
			requireHeader(t, x, got[key])
		case map[string]any:
			require.Len(t, got[key], len(x))
		}
	})
}

// complete serves PUT /orders/42 on a server with the webhook, with parameters as its on_complete event.
func complete(t *testing.T, parameters vlatte.Parameters) {
	t.Helper()

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Putf("/orders/42").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: parameters}).
		Reply(mockhttp.NoContent()))

	req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestWebHook_MultipartBodies(t *testing.T) {
	// Webhooks send multipart bodies built the same way as replies: from parameters, from multipart_template, which
	// renders with the request that completed the mock, and from Go. The target matches them with form patterns.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)

	tests := []struct {
		name       string
		targetMock *mockhttp.Builder
		parameters vlatte.Parameters
	}{
		{
			"parameters",
			mockhttp.Postf("/hooks").
				FormField("title", match.Eq("Holiday")).
				FormFile("doc", mockhttp.FileWith().Filename(match.Eq("a.txt")).Body(match.Eq("hello"))).
				Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "multipart": map[string]any{
				"type": "form-data",
				"parts": []any{
					map[string]any{"name": "title", "body": "Holiday"},
					map[string]any{"name": "doc", "filename": "a.txt", "body": "hello"},
				},
			}},
		},
		{
			"multipart_template",
			mockhttp.Postf("/hooks").FormField("order", match.Eq("42")).Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "multipart_template": map[string]any{
				"type":  "form-data",
				"parts": []any{map[string]any{"name": "order", "body": "{{ index .Request.URLPathSegments 1 }}"}},
			}},
		},
		{
			"Go",
			mockhttp.Postf("/hooks").
				FormFile("doc", mockhttp.FileWith().ContentType(match.Eq("application/json")).Body(match.JSONPath("$.id", match.Eq(1)))).
				Reply(mockhttp.OK()),
			Options().Method("POST").URL(target.URL("/hooks")).
				Multipart(mockhttp.MultipartForm(mockhttp.NewPart().Name("doc").Filename("b.json").BodyJSON(map[string]any{"id": 1}))).
				Build("webhook").Parameters,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer target.Clean(t.Context())

			scoped := target.MustMock(tc.targetMock)
			complete(t, tc.parameters)
			scoped.AssertNumberOfCalls(t, 1)
		})
	}
}

func TestWebHook_MultipartErrors(t *testing.T) {
	// Args built in code have no decoder to report conflicts, and templates parse at send time: Run fails, saying
	// why, before sending anything.
	wh, err := New()
	require.NoError(t, err)

	// Templates parse when the webhook sends, as body_template does.
	brokenTemplate, err := ArgsFromMap(map[string]any{
		"url":                "http://localhost",
		"multipart_template": map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "{{ .X"}}},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		args *Args
		want string
	}{
		{"two bodies", &Args{URL: "http://localhost", Body: "x", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart())}, "webhook: bad argument: more than one body is set: keep one of Body, BodyTemplate, BodyFile and Multipart"},
		{"a template that doesn't parse", brokenTemplate, "webhook: multipart body: multipart: parts[0]: body: template: body:1: unclosed action"},
		{"a Content-Type header", &Args{URL: "http://localhost", Header: http.Header{"content-type": {"x"}}, Multipart: mockhttp.MultipartMixed(mockhttp.NewPart())}, "webhook: bad argument: a multipart body sets the Content-Type: remove the Content-Type header"},
		{"a body that can't be written", &Args{URL: "http://localhost", Multipart: mockhttp.MultipartForm(mockhttp.NewPart())}, "webhook: multipart body: multipart: parts[0].name is required in a form-data part; name the form field"},
		{"a Go part with a delay", &Args{URL: "http://localhost", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart().Delay(time.Second))}, "webhook: multipart body: multipart: parts[0].delay can't be set: a body written at once doesn't pause; delays apply to replies only"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.EqualError(t, wh.Run(t.Context(), onComplete{params: tc.args}), tc.want)
		})
	}
}

func TestWebHook_RelativeFilesOpenFromTheMockFile(t *testing.T) {
	// A mock file's webhooks open relative files from the mock file's directory, as its replies do, wherever the
	// server runs: a whole body_file and a multipart part's alike. A document given as bytes has no directory, and
	// opens them from the working directory, as global events do, whatever mock they complete.
	got := make(chan string, 3)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + " " + string(b)
	}))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	global := vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{"method": "POST", "url": target.URL + "/global", "body_file": "body.txt"}}

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{global}}))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := fmt.Sprintf(`
request: {method: PUT, path: /orders/42}
response: {status: 204}
on_complete:
  - {name: webhook, parameters: {method: POST, url: "%[1]s/whole", body_file: body.txt}}
  - {name: webhook, parameters: {method: POST, url: "%[1]s/part", multipart: {type: mixed, boundary: b, parts: [{body_file: part.txt}]}}}
`, target.URL)

	mocks := t.TempDir()
	write(t, filepath.Join(mocks, "body.txt"), "body")
	write(t, filepath.Join(mocks, "part.txt"), "part")
	write(t, filepath.Join(mocks, "hook.mock.yaml"), doc)

	t.Chdir(t.TempDir())
	write(t, "body.txt", "cwd body")
	write(t, "part.txt", "cwd part")

	run := func(b vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) []string {
		scope := m.MustMock(b)
		defer scope.Clear(context.Background())

		req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		sent := []string{<-got, <-got, <-got}
		slices.Sort(sent)

		return sent
	}

	require.Equal(t, []string{"/global cwd body", "/part --b\r\n\r\npart\r\n--b--\r\n", "/whole body"},
		run(mockhttp.FromFile(filepath.Join(mocks, "hook.mock.yaml"))), "from the mock file's directory")
	require.Equal(t, []string{"/global cwd body", "/part --b\r\n\r\ncwd part\r\n--b--\r\n", "/whole cwd body"},
		run(mockhttp.FromBytes([]byte(doc), "yaml")), "from the working directory")
}

func write(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
}

// received is a multipart form as a webhook target parsed it.
type received struct {
	values map[string][]string
	files  map[string][]string // filename=content
}

func TestPropWebHook_MultipartReachesTheTarget(t *testing.T) {
	// W2: a multipart body, from parameters or from Go, reaches the target as the modeled form: its fields, and its
	// files with their names and content. Templated text renders with the request that completed the mock.
	got := make(chan received, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			got <- received{}

			return
		}

		files := make(map[string][]string)
		for name, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, _ := fh.Open()
				b, _ := io.ReadAll(f)
				f.Close()

				files[name] = append(files[name], fh.Filename+"="+string(b))
			}
		}

		got <- received{values: r.MultipartForm.Value, files: files}
	}))
	t.Cleanup(target.Close)

	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	token := rapid.StringMatching(`[A-Za-z0-9_.-]{1,8}`)

	rapid.Check(t, func(t *rapid.T) {
		templated := rapid.Bool().Draw(t, "templated")
		fromGo := !templated && rapid.Bool().Draw(t, "from Go")

		n := rapid.IntRange(1, 4).Draw(t, "parts")
		parts := make([]any, 0, n)
		goParts := make([]*mockhttp.Part, 0, n)
		want := received{values: make(map[string][]string), files: make(map[string][]string)}

		rendered := func(s string) string { return s }
		if templated {
			rendered = func(s string) string { return s + "PUT" }
		}

		for range n {
			name := token.Draw(t, "name")
			body := token.Draw(t, "body")

			text := body
			if templated {
				text += "{{ .Request.Method }}"
			}

			part := map[string]any{"name": name, "body": text}
			goPart := mockhttp.NewPart().Name(name).BodyText(text)

			if rapid.Bool().Draw(t, "file") {
				filename := token.Draw(t, "filename")
				part["filename"] = filename
				goPart.Filename(filename)
				want.files[name] = append(want.files[name], filename+"="+rendered(body))
			} else {
				want.values[name] = append(want.values[name], rendered(body))
			}

			parts = append(parts, part)
			goParts = append(goParts, goPart)
		}

		var parameters vlatte.Parameters

		switch {
		case fromGo:
			parameters = &Args{Method: "POST", URL: target.URL, Multipart: mockhttp.MultipartForm(goParts...)}
		case templated:
			parameters = map[string]any{"method": "POST", "url": target.URL, "multipart_template": map[string]any{"type": "form-data", "parts": parts}}
		default:
			parameters = map[string]any{"method": "POST", "url": target.URL, "multipart": map[string]any{"type": "form-data", "parts": parts}}
		}

		scope, err := m.Mock(mockhttp.Putf("/orders/42").
			OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: parameters}).
			Reply(mockhttp.NoContent()))
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		select {
		case r := <-got:
			require.Equal(t, want.values, r.values)
			require.Equal(t, want.files, r.files)
		case <-time.After(5 * time.Second):
			t.Fatalf("the webhook didn't reach the target")
		}
	})
}

// webhookFault is an invalid setting injected into valid multipart parameters, and the problem it must cause.
type webhookFault struct {
	area   string
	code   string
	key    string
	inject func(params map[string]any)
}

// _webhookFaults touch distinct settings of valid multipart parameters.
var _webhookFaults = []webhookFault{
	{"body", config.CodeConflict, "parameters.multipart", func(p map[string]any) { p["body"] = "x" }},
	{"template", config.CodeConflict, "parameters.multipart_template", func(p map[string]any) {
		// An object of its own: the faults of multipart mustn't show up twice.
		p["multipart_template"] = map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "z"}}}
	}},
	{"header", config.CodeConflict, "parameters.header.Content-Type", func(p map[string]any) { p["header"] = map[string]any{"Content-Type": "x"} }},
	{"delay", config.CodeNotAllowed, "parameters.multipart.parts[0].delay", func(p map[string]any) {
		multipartOf(p)["parts"].([]any)[0].(map[string]any)["delay"] = "1s"
	}},
	{"boundary", config.CodeInvalidValue, "parameters.multipart.boundary", func(p map[string]any) { multipartOf(p)["boundary"] = "a b " }},
	{"type", config.CodeMissingValue, "parameters.multipart.parts[1].name", func(p map[string]any) { multipartOf(p)["type"] = "form-data" }},
}

func multipartOf(params map[string]any) map[string]any {
	m, _ := params["multipart"].(map[string]any)
	return m
}

func TestPropArgsFromMap_EveryMultipartFaultOnce(t *testing.T) {
	// W3: each fault of multipart parameters is reported once, with its code and key, and nothing else is: one pass
	// fixes them all.
	rapid.Check(t, func(t *rapid.T) {
		params := map[string]any{
			"url": "http://localhost",
			"multipart": map[string]any{
				"type": "mixed",
				"parts": []any{
					map[string]any{"name": "a", "body": "x"},
					map[string]any{"body": "y"},
				},
			},
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_webhookFaults), 1, len(_webhookFaults),
			func(f webhookFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(params)
			want = append(want, f.code+" "+f.key)
		}

		_, err := ArgsFromMap(params)
		require.ErrorIs(t, err, ErrBadArg)

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok)

		got := make([]string, 0, len(cfgErr.Problems))
		for _, p := range cfgErr.Problems {
			got = append(got, p.Code+" "+p.Key)
		}

		slices.Sort(got)
		slices.Sort(want)
		require.Equal(t, want, got)
	})
}

func TestWebHook_PrepareFailsTheMock(t *testing.T) {
	// A webhook's parameters are checked, and its templates compiled, when its mock is built: a mistake fails the
	// mock instead of each request after the response is sent.
	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	defer m.Close()

	brokenPart := mockhttp.MultipartMixed(mockhttp.NewPart().BodyText("{{ .X")).Template()

	tests := []struct {
		name     string
		params   vlatte.Parameters
		sentinel error
		want     string
	}{
		{
			name:     "url_template",
			params:   map[string]any{"url_template": "{{ .X"},
			sentinel: ErrRenderingTemplate,
			want:     "template: url_template:1: unclosed action",
		},
		{
			name:     "header_template",
			params:   map[string]any{"url": "http://localhost", "header_template": map[string]any{"X-A": "{{ .X"}},
			sentinel: ErrRenderingTemplate,
			want:     "template: header_template X-A:1: unclosed action",
		},
		{
			name:     "body_template",
			params:   map[string]any{"url": "http://localhost", "body_template": "{{ .X"},
			sentinel: ErrRenderingTemplate,
			want:     "template: body_template:1: unclosed action",
		},
		{
			name: "multipart_template",
			params: map[string]any{"url": "http://localhost", "multipart_template": map[string]any{
				"type": "mixed", "parts": []any{map[string]any{"body": "{{ .X"}},
			}},
			want: "webhook: multipart body: multipart: parts[0]: body: template: body:1: unclosed action",
		},
		{
			name:   "a multipart body built in Go",
			params: &Args{URL: "http://localhost", Multipart: brokenPart},
			want:   "webhook: multipart body: multipart: parts[0]: body: template: body:1: unclosed action",
		},
		{
			name:     "a parameter that doesn't exist",
			params:   map[string]any{"url": "http://localhost", "urll": "x"},
			sentinel: ErrBadArg,
			want:     "urll",
		},
		// #21: a webhook without a URL built, then failed every send with "unsupported protocol scheme".
		{
			name:     "no url",
			params:   map[string]any{"method": "POST"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: one of url and url_template is required",
		},
		{
			name:     "no url in Go",
			params:   &Args{Method: "POST"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: one of url and url_template is required",
		},
		// Args built in Go have no decoder: the rules of mock files are checked when the mock builds, not at each send.
		{
			name:     "url and url_template in Go",
			params:   &Args{URL: "http://localhost", URLTemplate: "http://{{ .X }}"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: url and url_template are both set: keep one of them",
		},
		{
			name:     "two bodies in Go",
			params:   &Args{URL: "http://localhost", Body: "x", BodyFile: "x.txt"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: more than one body is set",
		},
		{
			name: "a multipart body and a Content-Type header in Go",
			params: &Args{
				URL: "http://localhost", HeaderTemplate: map[string][]string{"content-type": {"x"}},
				Multipart: mockhttp.MultipartMixed(mockhttp.NewPart()),
			},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: a multipart body sets the Content-Type: remove the Content-Type header",
		},
		// Header names ignore case: the same header twice had one of its values, at random.
		{
			name:     "a header in header and header_template",
			params:   map[string]any{"url": "http://localhost", "header": map[string]any{"X-A": "1"}, "header_template": map[string]any{"x-a": "2"}},
			sentinel: ErrBadArg,
			want:     "conflict: parameters.header_template.x-a is set with parameters.header.X-A; header names ignore case: keep one of them",
		},
		{
			name:     "a header twice in Go",
			params:   &Args{URL: "http://localhost", Header: http.Header{"x-a": {"1"}, "X-A": {"2"}}},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: headers X-A and x-a are one header: keep one of them",
		},
		// A URL or a method a request can't use failed every send.
		{
			name:     "a relative url",
			params:   map[string]any{"url": "/hooks"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: url: /hooks is not an absolute http or https URL",
		},
		{
			name:     "an ftp url, with a password",
			params:   map[string]any{"url": "ftp://user:secret@files.test/hooks"},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: url: ftp://user:xxxxx@files.test/hooks is not an absolute http or https URL",
		},
		{
			name:     "a url that doesn't parse",
			params:   map[string]any{"url": " -   " + string(rune(0x7f))},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: url: net/url: invalid control character in URL",
		},
		{
			name:     "a url with a password that doesn't parse",
			params:   map[string]any{"url": "http://user:secret@hooks.test/%zz"},
			sentinel: ErrBadArg,
			want:     `webhook: bad argument: url: invalid URL escape "%zz"`,
		},
		{
			name:     "a negative delay",
			params:   map[string]any{"url": "http://localhost", "delay": -1},
			sentinel: ErrBadArg,
			want:     "invalid_value: parameters.delay must be 0 or more",
		},
		{
			name:     "a negative delay in Go",
			params:   &Args{URL: "http://localhost", Delay: -time.Second},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: delay -1s is negative",
		},
		{
			name:     "a negative timeout",
			params:   map[string]any{"url": "http://localhost", "timeout": -1},
			sentinel: ErrBadArg,
			want:     "invalid_value: parameters.timeout must be 0 or more",
		},
		{
			name:     "a negative timeout in Go",
			params:   &Args{URL: "http://localhost", Timeout: -time.Second},
			sentinel: ErrBadArg,
			want:     "webhook: bad argument: timeout -1s is negative",
		},
		{
			name:     "a method that isn't one",
			params:   map[string]any{"url": "http://localhost", "method": "ghjk%&^*()"},
			sentinel: ErrBadArg,
			want:     `webhook: bad argument: method "ghjk%&^*()" is not an HTTP method`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(mockhttp.Getf("/x").
				OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: tc.params}).
				Reply(mockhttp.NoContent()))

			require.ErrorContains(t, err, "mock: on_complete listener webhook at index 0: ")
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "secret", "a URL's password stays out of errors")

			if tc.sentinel != nil {
				require.ErrorIs(t, err, tc.sentinel)
			}
		})
	}
}

func TestWebHook_TransformCanAddHeaders(t *testing.T) {
	// Parameters without headers had nil header maps, and a Transform that added a header panicked.
	rt := &sent{}
	wh, err := New(WithTransport(rt), WithTransform(func(_ context.Context, _ vlatte.OnComplete, a *Args) error {
		a.Header["X-A"] = []string{"a"}
		a.HeaderTemplate["X-B"] = []string{"{{ .b }}"}

		return nil
	}))
	require.NoError(t, err)

	for _, params := range []vlatte.Parameters{map[string]any{"url": "http://hooks.test"}, &Args{URL: "http://hooks.test"}} {
		rt.req = nil
		require.NoError(t, wh.Run(t.Context(), onComplete{params: params, kit: kit{data: map[string]any{"b": "b"}}}))
		require.Equal(t, "a", rt.req.Header.Get("X-A"))
		require.Equal(t, "b", rt.req.Header.Get("X-B"))
	}
}

func TestWebHook_RunChecksWhatTheSendUses(t *testing.T) {
	// The mock's parameters were checked when it was built; what a Transform or a url_template makes of them is
	// checked before the send.
	tests := []struct {
		name      string
		params    vlatte.Parameters
		transform Transform
		want      string
	}{
		{
			name:   "a url_template that renders a relative URL",
			params: map[string]any{"url_template": "{{ .path }}"},
			want:   "webhook: url_template: /hooks is not an absolute http or https URL",
		},
		{
			name:   "a Transform that removes the URL",
			params: &Args{URL: "http://hooks.test"},
			transform: func(_ context.Context, _ vlatte.OnComplete, a *Args) error {
				a.URL = ""
				return nil
			},
			want: "webhook: bad argument: one of url and url_template is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &sent{}
			options := []Option{WithTransport(rt)}
			if tc.transform != nil {
				options = append(options, WithTransform(tc.transform))
			}

			wh, err := New(options...)
			require.NoError(t, err)

			err = wh.Run(t.Context(), onComplete{params: tc.params, kit: kit{data: map[string]any{"path": "/hooks"}}})
			require.EqualError(t, err, tc.want)
			require.Nil(t, rt.req, "nothing is sent")
		})
	}
}

func TestArgsBuilder_EachConfigHasItsOwnArgs(t *testing.T) {
	// Build gave every config the builder's own *Args: changing the builder changed the configs built before.
	b := Options().URL("http://a.test").Header("X-A", "1")
	first := b.Build("webhook")

	b.URL("http://b.test").Header("X-A", "2")
	second := b.Build("webhook")

	require.Equal(t, &Args{URL: "http://a.test", Header: http.Header{"X-A": {"1"}}}, first.Parameters)
	require.Equal(t, &Args{URL: "http://b.test", Header: http.Header{"X-A": {"1", "2"}}}, second.Parameters)
}

func TestWebHook_PrepareFailsAMockFile(t *testing.T) {
	wh, err := New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	defer m.Close()

	doc := "request: {path: /x}\non_complete: [{name: webhook, parameters: {url_template: '{{ .X'}}]\n"

	_, err = m.Mock(mockhttp.FromBytes([]byte(doc), "yaml"))
	require.ErrorContains(t, err, "template: url_template:1: unclosed action")
}

func TestWebHook_PrepareFailsTheServerOnBadGlobalEvents(t *testing.T) {
	// Global events run for every mock, so their parameters are checked when the server is built. The config keeps
	// the parameters as they were given.
	wh, err := New()
	require.NoError(t, err)

	global := &vlatte.Events{OnComplete: []vlatte.EventConfig{
		{Name: "webhook", Parameters: map[string]any{"url_template": "{{ .X"}},
	}}

	_, err = mockhttp.New(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh).GlobalEvents(global))
	require.ErrorContains(t, err, "server: global on_complete listener webhook at index 0: ")
	require.ErrorIs(t, err, ErrRenderingTemplate)
	require.IsType(t, make(map[string]any), global.OnComplete[0].Parameters)
}

func TestWebHook_TransformChangesOneSend(t *testing.T) {
	// Transform edits the parameters of one send. Each send starts from the mock's parameters again, so changes don't
	// pile up across requests, and a template Transform sets is honored.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	target.MustMock(mockhttp.Postf("/hooks").
		Headerf("X-Seen", "x").
		Body(match.Eq("GET transformed")).
		Reply(mockhttp.OK()))

	wh, err := New(WithTransform(func(_ context.Context, _ vlatte.OnComplete, a *Args) error {
		a.Header["X-Seen"][0] += "x"
		a.BodyTemplate = "{{ .Request.Method }} transformed"

		return nil
	}))
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	defer m.Close()

	args := &Args{URL: target.URL("/hooks"), Method: http.MethodPost, Header: http.Header{"X-Seen": {""}}}
	m.MustMock(mockhttp.Getf("/x").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: args}).
		Reply(mockhttp.NoContent()))

	send := func() {
		res, err := http.Get(m.URL("/x"))
		if assert.NoError(t, err) {
			assert.Equal(t, http.StatusNoContent, res.StatusCode)
			assert.NoError(t, res.Body.Close())
		}
	}

	send()
	send()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(send)
	}

	wg.Wait()

	require.True(t, target.AssertNumberOfCalls(t, 10))
	require.Equal(t, []string{""}, args.Header["X-Seen"], "the mock's parameters stay as they were given")
	require.Empty(t, args.BodyTemplate)
}

// loadSchema compiles the JSON schema of mock files, and returns it with its document.
func loadSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()

	b, err := os.ReadFile("../mockhttp/schema/http.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource("mem://vlatte/http.schema.json", doc))

	schema, err := c.Compile("mem://vlatte/http.schema.json")
	require.NoError(t, err)

	raw, ok := doc.(map[string]any)
	require.True(t, ok)

	return schema, raw
}

func TestJSONSchema_WebhookParametersAreTheDecodersKeys(t *testing.T) {
	// Editors check the parameters of webhooks with the schema of mock files: it documents every key the webhook
	// decodes, and nothing else.
	_, raw := loadSchema(t)

	defs, _ := raw["$defs"].(map[string]any)
	params, _ := defs["webhook_parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)

	require.Equal(t, _argsSchema.Keys(), slices.Sorted(maps.Keys(props)))
}

func TestJSONSchema_AcceptsWhatPrepareAccepts(t *testing.T) {
	// An editor marks a webhook's parameters as wrong when building its mock fails, but for what only building the mock
	// finds: header names equal but for case, as JSON Schema can't compare property names that way, and the syntax of a
	// url past its scheme and host, which a pattern can't check as url.Parse does.
	schema, _ := loadSchema(t)
	engine := vlatte.MustNewGoTemplateEngine()
	multipart := map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "a"}}}

	tests := []struct {
		name        string
		params      map[string]any
		prepareOnly bool // building the mock rejects it, and JSON Schema can't tell
	}{
		{name: "a url", params: map[string]any{"url": "http://hooks.test/x"}},
		{name: "every kind of key", params: map[string]any{
			"url_template": "http://hooks.test/{{ .Request.Method }}", "method": "post",
			"header": map[string]any{"X-A": "1"}, "header_template": map[string]any{"X-B": "{{ .Request.Method }}"},
			"body_template": "{{ .Request.Method }}", "delay": "1.5s",
		}},
		{name: "a multipart body", params: map[string]any{"url": "http://hooks.test", "multipart": multipart}},
		{name: "a delay in milliseconds", params: map[string]any{"url": "http://hooks.test", "delay": 1500}},
		{name: "no url", params: map[string]any{"method": "POST"}},
		{name: "url and url_template", params: map[string]any{"url": "http://hooks.test", "url_template": "http://hooks.test"}},
		{name: "a relative url", params: map[string]any{"url": "/hooks"}},
		{name: "an ftp url", params: map[string]any{"url": "ftp://files.test"}},
		{name: "a method that isn't one", params: map[string]any{"url": "http://hooks.test", "method": "ghjk%&^*()"}},
		{name: "two bodies", params: map[string]any{"url": "http://hooks.test", "body": "x", "body_file": "x.txt"}},
		{name: "a body in base64", params: map[string]any{"url": "http://hooks.test", "body_base64": "//4A\n//4A"}},
		{name: "a body in base64 without its padding", params: map[string]any{"url": "http://hooks.test", "body_base64": "//4"}},
		{name: "a body in base64 and a body file", params: map[string]any{"url": "http://hooks.test", "body_base64": "//4A", "body_file": "x.txt"}},
		{name: "a body in base64 and a multipart body", params: map[string]any{"url": "http://hooks.test", "body_base64": "//4A", "multipart": multipart}},
		{name: "a multipart body and a Content-Type header", params: map[string]any{
			"url": "http://hooks.test", "header": map[string]any{"content-type": "x"}, "multipart": multipart,
		}},
		{name: "a list as a header value", params: map[string]any{"url": "http://hooks.test", "header": map[string]any{"X-A": []any{"1", "2"}}}},
		{name: "a list holding a number as a header value", params: map[string]any{
			"url": "http://hooks.test", "header_template": map[string]any{"X-A": []any{"1", 2}},
		}},
		{name: "a number as a header value", params: map[string]any{"url": "http://hooks.test", "header": map[string]any{"X-A": 1}}},
		{name: "a negative delay", params: map[string]any{"url": "http://hooks.test", "delay": -1}},
		{name: "a key that doesn't exist", params: map[string]any{"url": "http://hooks.test", "urll": "x"}},
		{name: "an uppercase scheme", params: map[string]any{"url": "HTTP://hooks.test"}},
		{name: "a mixed-case scheme", params: map[string]any{"url": "Https://hooks.test/x"}},
		{name: "a host with an unclosed bracket", params: map[string]any{"url": "http://[::1"}, prepareOnly: true},
		{name: "a port that isn't a number", params: map[string]any{"url": "http://hooks.test:port"}, prepareOnly: true},
		{name: "header names equal but for case", params: map[string]any{
			"url": "http://hooks.test", "header": map[string]any{"X-A": "1", "x-a": "2"},
		}, prepareOnly: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, prepareErr := (&WebHook{}).Prepare(vlatte.EventConfig{Name: "webhook", Parameters: tc.params}, engine)

			doc, err := json.Marshal(map[string]any{
				"request":     map[string]any{"path": "/x"},
				"response":    map[string]any{"status": 204},
				"on_complete": []any{map[string]any{"name": "webhook", "parameters": tc.params}},
			})
			require.NoError(t, err)

			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
			require.NoError(t, err)

			schemaErr := schema.Validate(instance)
			if tc.prepareOnly {
				require.Error(t, prepareErr)
				require.NoError(t, schemaErr)

				return
			}

			require.Equal(t, prepareErr == nil, schemaErr == nil, "Prepare: %v\nschema: %v", prepareErr, schemaErr)
		})
	}
}
