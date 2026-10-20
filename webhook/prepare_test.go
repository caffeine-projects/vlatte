package webhook_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

func TestWebHook_PrepareFailsTheMock(t *testing.T) {
	// A webhook's parameters are checked, and its templates compiled, when its mock is built: a mistake fails the
	// mock instead of each request after the response is sent.
	wh, err := webhook.New()
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
			sentinel: webhook.ErrRenderingTemplate,
			want:     "template: url_template:1: unclosed action",
		},
		{
			name:     "header_template",
			params:   map[string]any{"url": "http://localhost", "header_template": map[string]any{"X-A": "{{ .X"}},
			sentinel: webhook.ErrRenderingTemplate,
			want:     "template: header_template X-A:1: unclosed action",
		},
		{
			name:     "body_template",
			params:   map[string]any{"url": "http://localhost", "body_template": "{{ .X"},
			sentinel: webhook.ErrRenderingTemplate,
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
			params: &webhook.Args{URL: "http://localhost", Multipart: brokenPart},
			want:   "webhook: multipart body: multipart: parts[0]: body: template: body:1: unclosed action",
		},
		{
			name:     "a parameter that doesn't exist",
			params:   map[string]any{"url": "http://localhost", "urll": "x"},
			sentinel: webhook.ErrBadArg,
			want:     "urll",
		},
		// #21: a webhook without a URL built, then failed every send with "unsupported protocol scheme".
		{
			name:     "no url",
			params:   map[string]any{"method": "POST"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: one of url and url_template is required",
		},
		{
			name:     "no url in Go",
			params:   &webhook.Args{Method: "POST"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: one of url and url_template is required",
		},
		// Args built in Go have no decoder: the rules of mock files are checked when the mock builds, not at each send.
		{
			name:     "url and url_template in Go",
			params:   &webhook.Args{URL: "http://localhost", URLTemplate: "http://{{ .X }}"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: url and url_template are both set: keep one of them",
		},
		{
			name:     "two bodies in Go",
			params:   &webhook.Args{URL: "http://localhost", Body: "x", BodyFile: "x.txt"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: more than one body is set",
		},
		{
			name: "a multipart body and a Content-Type header in Go",
			params: &webhook.Args{
				URL: "http://localhost", HeaderTemplate: map[string]string{"content-type": "x"},
				Multipart: mockhttp.MultipartMixed(mockhttp.NewPart()),
			},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: a multipart body sets the Content-Type: remove the Content-Type header",
		},
		// Header names ignore case: the same header twice had one of its values, at random.
		{
			name:     "a header in header and header_template",
			params:   map[string]any{"url": "http://localhost", "header": map[string]any{"X-A": "1"}, "header_template": map[string]any{"x-a": "2"}},
			sentinel: webhook.ErrBadArg,
			want:     "conflict: parameters.header_template.x-a is set with parameters.header.X-A; header names ignore case: keep one of them",
		},
		{
			name:     "a header twice in Go",
			params:   &webhook.Args{URL: "http://localhost", Header: map[string]string{"x-a": "1", "X-A": "2"}},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: headers X-A and x-a are one header: keep one of them",
		},
		// A URL or a method a request can't use failed every send.
		{
			name:     "a relative url",
			params:   map[string]any{"url": "/hooks"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: url: /hooks is not an absolute http or https URL",
		},
		{
			name:     "an ftp url, with a password",
			params:   map[string]any{"url": "ftp://user:secret@files.test/hooks"},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: url: ftp://user:xxxxx@files.test/hooks is not an absolute http or https URL",
		},
		{
			name:     "a url that doesn't parse",
			params:   map[string]any{"url": " -   " + string(rune(0x7f))},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: url: net/url: invalid control character in URL",
		},
		{
			name:     "a url with a password that doesn't parse",
			params:   map[string]any{"url": "http://user:secret@hooks.test/%zz"},
			sentinel: webhook.ErrBadArg,
			want:     `webhook: bad argument: url: invalid URL escape "%zz"`,
		},
		{
			name:     "a negative delay",
			params:   map[string]any{"url": "http://localhost", "delay": -1},
			sentinel: webhook.ErrBadArg,
			want:     "invalid_value: parameters.delay must be 0 or more",
		},
		{
			name:     "a negative delay in Go",
			params:   &webhook.Args{URL: "http://localhost", Delay: -time.Second},
			sentinel: webhook.ErrBadArg,
			want:     "webhook: bad argument: delay -1s is negative",
		},
		{
			name:     "a method that isn't one",
			params:   map[string]any{"url": "http://localhost", "method": "ghjk%&^*()"},
			sentinel: webhook.ErrBadArg,
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
	wh, err := webhook.New(webhook.WithTransport(rt), webhook.WithTransform(func(_ context.Context, _ vlatte.OnComplete, a *webhook.Args) error {
		a.Header["X-A"] = "a"
		a.HeaderTemplate["X-B"] = "{{ .b }}"

		return nil
	}))
	require.NoError(t, err)

	for _, params := range []vlatte.Parameters{map[string]any{"url": "http://hooks.test"}, &webhook.Args{URL: "http://hooks.test"}} {
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
		transform webhook.Transform
		want      string
	}{
		{
			name:   "a url_template that renders a relative URL",
			params: map[string]any{"url_template": "{{ .path }}"},
			want:   "webhook: url_template: /hooks is not an absolute http or https URL",
		},
		{
			name:   "a Transform that removes the URL",
			params: &webhook.Args{URL: "http://hooks.test"},
			transform: func(_ context.Context, _ vlatte.OnComplete, a *webhook.Args) error {
				a.URL = ""
				return nil
			},
			want: "webhook: bad argument: one of url and url_template is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &sent{}
			options := []webhook.Option{webhook.WithTransport(rt)}
			if tc.transform != nil {
				options = append(options, webhook.WithTransform(tc.transform))
			}

			wh, err := webhook.New(options...)
			require.NoError(t, err)

			err = wh.Run(t.Context(), onComplete{params: tc.params, kit: kit{data: map[string]any{"path": "/hooks"}}})
			require.EqualError(t, err, tc.want)
			require.Nil(t, rt.req, "nothing is sent")
		})
	}
}

func TestArgsBuilder_EachConfigHasItsOwnArgs(t *testing.T) {
	// Build gave every config the builder's own *Args: changing the builder changed the configs built before.
	b := webhook.Options().URL("http://a.test").Header("X-A", "1")
	first := b.Build("webhook")

	b.URL("http://b.test").Header("X-A", "2")
	second := b.Build("webhook")

	require.Equal(t, &webhook.Args{URL: "http://a.test", Header: map[string]string{"X-A": "1"}}, first.Parameters)
	require.Equal(t, &webhook.Args{URL: "http://b.test", Header: map[string]string{"X-A": "2"}}, second.Parameters)
}

func TestWebHook_PrepareFailsAMockFile(t *testing.T) {
	wh, err := webhook.New()
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
	wh, err := webhook.New()
	require.NoError(t, err)

	global := &vlatte.Events{OnComplete: []vlatte.EventConfig{
		{Name: "webhook", Parameters: map[string]any{"url_template": "{{ .X"}},
	}}

	_, err = mockhttp.New(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh).GlobalEvents(global))
	require.ErrorContains(t, err, "server: global on_complete listener webhook at index 0: ")
	require.ErrorIs(t, err, webhook.ErrRenderingTemplate)
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

	wh, err := webhook.New(webhook.WithTransform(func(_ context.Context, _ vlatte.OnComplete, a *webhook.Args) error {
		a.Header["X-Seen"] += "x"
		a.BodyTemplate = "{{ .Request.Method }} transformed"

		return nil
	}))
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	defer m.Close()

	args := &webhook.Args{URL: target.URL("/hooks"), Method: http.MethodPost, Header: map[string]string{"X-Seen": ""}}
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
	require.Empty(t, args.Header["X-Seen"], "the mock's parameters stay as they were given")
	require.Empty(t, args.BodyTemplate)
}
