package webhook_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(mockhttp.Getf("/x").
				OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: tc.params}).
				Reply(mockhttp.NoContent()))

			require.ErrorContains(t, err, "mock: on_complete listener webhook at index 0: ")
			require.ErrorContains(t, err, tc.want)

			if tc.sentinel != nil {
				require.ErrorIs(t, err, tc.sentinel)
			}
		})
	}
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
